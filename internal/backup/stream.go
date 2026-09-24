package backup

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
)

const (
	backupStreamMagic  = "PKRRBK03"
	backupChunkSize    = 1 << 20 // 1 MiB bounded plaintext/ciphertext working set
	streamNoncePrefix  = 8
	maxLegacyBytes     = 1 << 30 // legacy v1/v2 still require one bounded buffer
	maxStreamChunkSize = 16 << 20
)

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
// Only legacy archives use a bounded whole-file compatibility buffer.
func DecryptStream(dst io.Writer, src io.Reader, key string) error {
	br := bufio.NewReader(src)
	magic, err := br.Peek(len(backupStreamMagic))
	if err == nil && bytes.Equal(magic, []byte(backupStreamMagic)) {
		return decryptV3(dst, br, key)
	}
	legacy, err := io.ReadAll(io.LimitReader(br, maxLegacyBytes+1))
	if err != nil {
		return err
	}
	if len(legacy) > maxLegacyBytes {
		return fmt.Errorf("legacy backup exceeds the %d-byte compatibility limit", maxLegacyBytes)
	}
	plain, err := Decrypt(legacy, key)
	if err != nil {
		return err
	}
	return writeBackupBytes(dst, plain)
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

func decryptArchiveFile(ctx context.Context, encryptedPath, key string) (string, error) {
	in, err := os.Open(encryptedPath) // #nosec G304 -- operator/configured backup path
	if err != nil {
		return "", err
	}
	defer in.Close()
	out, err := os.CreateTemp("", "parkrr-restore-*.dump")
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

// ValidateFile validates an encrypted archive using bounded memory.
func ValidateFile(ctx context.Context, encryptedPath, key string) (ArchiveInfo, error) {
	plainPath, err := decryptArchiveFile(ctx, encryptedPath, key)
	if err != nil {
		return ArchiveInfo{}, err
	}
	defer os.Remove(plainPath)
	toc, err := plainArchiveTOCFile(ctx, plainPath)
	if err != nil {
		return ArchiveInfo{}, err
	}
	return parseTOC(toc), nil
}

// RestoreFile decrypts, validates and restores an archive using temporary files
// rather than retaining encrypted and plaintext copies in process memory.
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
	if _, err := plainArchiveTOCFile(ctx, plainPath); err != nil {
		return err
	}
	dsn, env := dbExecEnv(dbURL)
	// #nosec G204 -- fixed executable; DSN is operator configuration and the path
	// is an application-created temporary file.
	cmd := exec.CommandContext(ctx, "pg_restore", "--single-transaction", "--clean", "--if-exists",
		"--no-owner", "--no-privileges", "--dbname="+dsn, plainPath)
	cmd.Env = env
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("pg_restore failed: %w: %s", err, stderr.String())
	}
	return nil
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
