package backup

import (
	"context"
	"testing"
)

// TestS3TestUnconfigured: the connection test fails fast on an unconfigured bucket
// (no client is built, so no network call). The live BucketExists path needs a real
// S3 endpoint and is exercised via the handler against the operator's bucket.
func TestS3TestUnconfigured(t *testing.T) {
	if err := TestS3(context.Background(), S3Config{}); err == nil {
		t.Error("TestS3 on an unconfigured S3Config must return an error")
	}
}
