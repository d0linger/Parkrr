package backup

import (
	"bufio"
	"bytes"
	"context"
	"crypto/aes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync/atomic"
)

const (
	backupStreamMagic  = "PKRRBK03"
	backupChunkSize    = 1 << 20 // 1 MiB bounded plaintext/ciphertext working set
	streamNoncePrefix  = 8
	maxStreamChunkSize = 16 << 20

	// Legacy v1/v2 archives authenticate the whole file at once and therefore
	// need one whole-file buffer (decrypted in place, so one copy, not two).
	// The server keeps that buffer small enough for the hardened 256 MiB memory
	// limit; the offline CLI may raise it up to maxLegacyBytes.
	defaultLegacyBytes = 64 << 20
	maxLegacyBytes     = 1 << 30
)

var legacyLimit atomic.Int64

// SetLegacyArchiveLimit raises (or lowers) the in-memory limit for legacy v1/v2
// archives. Only the offline `parkrr restore` CLI calls it; the server keeps the
// memory-safe default.
func SetLegacyArchiveLimit(n int64) {
	legacyLimit.Store(min(max(n, 1), maxLegacyBytes))
}

// legacyArchiveLimit is the byte cap for buffered legacy (v1/v2) archives.
func legacyArchiveLimit() int64 {
	if n := legacyLimit.Load(); n > 0 {
		return n
	}
	return defaultLegacyBytes
}

type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	select {
	case <-r.ctx.Done():
		return 0, r.ctx.Err()
	default:
		return r.r.Read(p)
	}
}

func streamAAD(header []byte, counter, size uint32) []byte {
	aad := make([]byte, len(header)+8)
	copy(aad, header)
	binary.BigEndian.PutUint32(aad[len(header):], counter)
	binary.BigEndian.PutUint32(aad[len(header)+4:], size)
	return aad
}

func streamNonce(prefix []byte, counter uint32) []byte {
	nonce := make([]byte, streamNoncePrefix+4)
	copy(nonce, prefix)
	binary.BigEndian.PutUint32(nonce[streamNoncePrefix:], counter)
	return nonce
}

func writeBackupBytes(dst io.Writer, p []byte) error {
	for len(p) > 0 {
		n, err := dst.Write(p)
		if err != nil {
			return err
		}
		if n <= 0 || n > len(p) {
			return io.ErrShortWrite
		}
		p = p[n:]
	}
	return nil
}

// EncryptStream writes the v3 chunked archive format. Each frame is independently
// authenticated and a final authenticated zero-length frame detects truncation at
// an otherwise valid chunk boundary.
func EncryptStream(dst io.Writer, src io.Reader, key string) error {
	salt := make([]byte, backupSaltSize)
	noncePrefix := make([]byte, streamNoncePrefix)
	if _, err := io.ReadFull(rand.Reader, salt); err != nil {
		return err
	}
	if _, err := io.ReadFull(rand.Reader, noncePrefix); err != nil {
		return err
	}
	aead, err := aeadV2(key, salt)
	if err != nil {
		return err
	}
	header := make([]byte, 0, len(backupStreamMagic)+backupSaltSize+streamNoncePrefix+4)
	header = append(header, backupStreamMagic...)
	header = append(header, salt...)
	header = append(header, noncePrefix...)
	header = binary.BigEndian.AppendUint32(header, backupChunkSize)
	if err := writeBackupBytes(dst, header); err != nil {
		return err
	}

	plain := make([]byte, backupChunkSize)
	var counter uint32
	writeFrame := func(payload []byte) error {
		if counter == ^uint32(0) {
			return errors.New("backup stream has too many chunks")
		}
		if uint64(len(payload)) > uint64(^uint32(0)) {
			return errors.New("backup stream chunk is too large")
		}
		size := uint32(len(payload)) // #nosec G115 -- explicitly bounded above
		var frameHeader [4]byte
		binary.BigEndian.PutUint32(frameHeader[:], size)
		if err := writeBackupBytes(dst, frameHeader[:]); err != nil {
			return err
		}
		sealed := aead.Seal(nil, streamNonce(noncePrefix, counter), payload,
			streamAAD(header, counter, size))
		if err := writeBackupBytes(dst, sealed); err != nil {
			return err
		}
		counter++
		return nil
	}
	for {
		n, readErr := io.ReadFull(src, plain)
		if n > 0 {
			if err := writeFrame(plain[:n]); err != nil {
				return err
			}
		}
		switch readErr {
		case nil:
			continue
		case io.EOF, io.ErrUnexpectedEOF:
			return writeFrame(nil)
		default:
			return readErr
		}
	}
}

func decryptV3(dst io.Writer, src io.Reader, key string) error {
	headerLen := len(backupStreamMagic) + backupSaltSize + streamNoncePrefix + 4
	header := make([]byte, headerLen)
	if _, err := io.ReadFull(src, header); err != nil {
		return errors.New("backup file is too short or corrupt")
	}
	if !bytes.Equal(header[:len(backupStreamMagic)], []byte(backupStreamMagic)) {
		return errors.New("invalid streaming backup header")
	}
	saltStart := len(backupStreamMagic)
	prefixStart := saltStart + backupSaltSize
	chunkSize := binary.BigEndian.Uint32(header[prefixStart+streamNoncePrefix:])
	if chunkSize < 4096 || chunkSize > maxStreamChunkSize {
		return errors.New("invalid backup chunk size")
	}
	aead, err := aeadV2(key, header[saltStart:prefixStart])
	if err != nil {
		return err
	}
	noncePrefix := header[prefixStart : prefixStart+streamNoncePrefix]
	var counter uint32
	for {
		var sizeBytes [4]byte
		if _, err := io.ReadFull(src, sizeBytes[:]); err != nil {
			return errors.New("backup stream is truncated before its authenticated terminator")
		}
		size := binary.BigEndian.Uint32(sizeBytes[:])
		if size > chunkSize {
			return errors.New("backup frame exceeds declared chunk size")
		}
		ciphertext := make([]byte, int(size)+aead.Overhead())
		if _, err := io.ReadFull(src, ciphertext); err != nil {
			return errors.New("backup stream contains a truncated frame")
		}
		plain, err := aead.Open(nil, streamNonce(noncePrefix, counter), ciphertext,
			streamAAD(header, counter, size))
		if err != nil {
			return fmt.Errorf("decrypt failed (wrong key or corrupt file): %w", err)
		}
		counter++
		if size == 0 {
			var extra [1]byte
			n, readErr := src.Read(extra[:])
			if n != 0 || (readErr != nil && !errors.Is(readErr, io.EOF)) || readErr == nil {
				return errors.New("backup stream has trailing data")
			}
			return nil
		}
		if err := writeBackupBytes(dst, plain); err != nil {
			return err
		}
	}
}

// DecryptStream accepts the streaming v3 format and the existing v1/v2 formats.
// Only legacy archives use a bounded whole-file compatibility buffer, and only
// after their format was recognized: anything else is rejected before a single
// byte is buffered (BAK-07).
func DecryptStream(dst io.Writer, src io.Reader, key string) error {
	br := bufio.NewReader(src)
	magic, err := br.Peek(len(backupStreamMagic))
	if err == nil && bytes.Equal(magic, []byte(backupStreamMagic)) {
		return decryptV3(dst, br, key)
	}
	if key == "" {
		return errors.New("backup key is not set")
	}
	if err != nil || !bytes.Equal(magic, []byte(backupMagic)) {
		// v1 has no header. Recognize it by its content instead: the payload is
		// always a pg_dump custom archive, and GCM is counter mode, so the first
		// plaintext bytes can be derived without authenticating the whole file.
		// The full GCM check still follows; this only decides whether buffering
		// is worth it at all.
		head, _ := br.Peek(v1NonceSize + len(pgDumpMagic))
		if !looksLikeV1(head, key) {
			return errors.New("not a Parkrr backup archive, or the backup key does not match")
		}
	}
	limit := legacyArchiveLimit()
	legacy, err := io.ReadAll(io.LimitReader(br, limit+1))
	if err != nil {
		return err
	}
	if int64(len(legacy)) > limit {
		return fmt.Errorf("legacy (v1/v2) backup is larger than the %d MiB the server decrypts in memory; "+
			"restore it offline with `parkrr restore`, then create a new backup", limit>>20)
	}
	plain, err := openLegacy(legacy, key, true)
	if err != nil {
		return err
	}
	return writeBackupBytes(dst, plain)
}

const (
	v1NonceSize = 12 // cipher.NewGCM default
	pgDumpMagic = "PGDMP"
)

// looksLikeV1 derives the first plaintext bytes of a v1 archive (nonce ||
// AES-GCM ciphertext) and compares them with the pg_dump magic. GCM encrypts the
// first block with counter nonce||2 (counter 1 masks the tag).
func looksLikeV1(head []byte, key string) bool {
	if len(head) < v1NonceSize+len(pgDumpMagic) {
		return false
	}
	sum := sha256.Sum256([]byte(keyContext + key))
	block, err := aes.NewCipher(sum[:])
	if err != nil {
		return false
	}
	var counter, stream [aes.BlockSize]byte
	copy(counter[:], head[:v1NonceSize])
	binary.BigEndian.PutUint32(counter[v1NonceSize:], 2)
	block.Encrypt(stream[:], counter[:])
	for i := range len(pgDumpMagic) {
		if head[v1NonceSize+i]^stream[i] != pgDumpMagic[i] {
			return false
		}
	}
	return true
}

// DumpEncrypted streams pg_dump through chunked encryption into dst.
func DumpEncrypted(ctx context.Context, dbURL, key string, dst io.Writer) error {
	dsn, env := dbExecEnv(dbURL)
	// #nosec G204 -- fixed executable/arguments; DSN is operator configuration.
	cmd := exec.CommandContext(ctx, "pg_dump", "--format=custom", "--no-owner", "--no-privileges",
		"--exclude-table-data=sessions", "--exclude-schema=parkrr_control", "--dbname="+dsn)
	cmd.Env = env
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	encryptErr := EncryptStream(dst, contextReader{ctx: ctx, r: stdout}, key)
	if encryptErr != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
	waitErr := cmd.Wait()
	// A failed encryption killed pg_dump, so its exit status only echoes that
	// kill; report the encryption error that caused it.
	if encryptErr != nil {
		return fmt.Errorf("encrypt pg_dump: %w", encryptErr)
	}
	if waitErr != nil {
		return fmt.Errorf("pg_dump failed: %w: %s", waitErr, stderr.String())
	}
	return nil
}

// decryptArchiveFile decrypts an archive into a work file (pg_restore needs a
// seekable input) and returns its path; the caller removes it.
func decryptArchiveFile(ctx context.Context, encryptedPath, key string) (string, error) {
	in, err := os.Open(encryptedPath) // #nosec G304 -- operator/configured backup path
	if err != nil {
		return "", err
	}
	defer in.Close()
	out, err := createWorkFile("parkrr-restore-", ".dump")
	if err != nil {
		return "", err
	}
	path := out.Name()
	ok := false
	defer func() {
		_ = out.Close()
		if !ok {
			_ = os.Remove(path)
		}
	}()
	if err := DecryptStream(out, contextReader{ctx: ctx, r: in}, key); err != nil {
		return "", err
	}
	if err := out.Sync(); err != nil {
		return "", err
	}
	if err := out.Close(); err != nil {
		return "", err
	}
	ok = true
	return path, nil
}

func plainArchiveTOCFile(ctx context.Context, path string) (string, error) {
	// #nosec G204 -- fixed executable; path is an application-created temp file.
	cmd := exec.CommandContext(ctx, "pg_restore", "--list", path)
	out, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && len(exitErr.Stderr) > 0 {
			return "", fmt.Errorf("not a valid pg_dump archive: %w: %s", err, string(exitErr.Stderr))
		}
		return "", fmt.Errorf("not a valid pg_dump archive: %w", err)
	}
	return string(out), nil
}

// discardAfterError forwards to w until the first write error and then silently
// drops the rest. pg_restore --list exits once it has read the table of contents;
// the remaining frames must still be decrypted so that every chunk of the archive
// is authenticated, not just its head.
type discardAfterError struct {
	w   io.Writer
	err error
}

// Write forwards p until the first error and then swallows the rest, so the
// decrypting side keeps authenticating every frame.
func (d *discardAfterError) Write(p []byte) (int, error) {
	if d.err == nil {
		if _, err := d.w.Write(p); err != nil {
			d.err = err
		}
	}
	return len(p), nil
}

// archiveTOCFile decrypts an encrypted archive straight into `pg_restore --list`
// on stdin. No plaintext copy of the database touches the disk during validation
// or the nightly verify (BAK-06), and no temporary space is needed for it.
func archiveTOCFile(ctx context.Context, encryptedPath, key string) (string, error) {
	in, err := os.Open(encryptedPath) // #nosec G304 -- operator/configured backup path
	if err != nil {
		return "", err
	}
	defer in.Close()
	// #nosec G204 -- fixed executable and arguments; the archive arrives on stdin.
	cmd := exec.CommandContext(ctx, "pg_restore", "--list")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return "", err
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("not a valid pg_dump archive: %w", err)
	}
	decErr := DecryptStream(&discardAfterError{w: stdin}, contextReader{ctx: ctx, r: in}, key)
	_ = stdin.Close()
	waitErr := cmd.Wait()
	if decErr != nil {
		return "", decErr
	}
	if waitErr != nil {
		if stderr.Len() > 0 {
			return "", fmt.Errorf("not a valid pg_dump archive: %w: %s", waitErr, tail(stderr.String()))
		}
		return "", fmt.Errorf("not a valid pg_dump archive: %w", waitErr)
	}
	return stdout.String(), nil
}

// ValidateFile validates an encrypted archive using bounded memory.
func ValidateFile(ctx context.Context, encryptedPath, key string) (ArchiveInfo, error) {
	toc, err := archiveTOCFile(ctx, encryptedPath, key)
	if err != nil {
		return ArchiveInfo{}, err
	}
	return parseTOC(toc), nil
}

// RestoreFile decrypts, validates and restores an archive through a private file
// in the work directory (pg_restore needs seekable input for a reliable restore)
// rather than retaining encrypted and plaintext copies in process memory. The
// public schema is replaced as a whole (see restoreArchive).
func RestoreFile(ctx context.Context, dbURL, encryptedPath, key string) error {
	if err := acquireRun(ctx); err != nil {
		return err
	}
	defer releaseRun()
	plainPath, err := decryptArchiveFile(ctx, encryptedPath, key)
	if err != nil {
		return err
	}
	defer os.Remove(plainPath)
	toc, err := plainArchiveTOCFile(ctx, plainPath)
	if err != nil {
		return err
	}
	if err := checkRestorableTOC(toc); err != nil {
		return err
	}
	return restoreArchive(ctx, dbURL, plainPath, nil)
}

// ChecksumFile returns SHA-256 and size without loading the archive.
func ChecksumFile(path string) (string, int64, error) {
	f, err := os.Open(path) // #nosec G304 -- caller owns the backup path
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}
