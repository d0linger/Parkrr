package server

import (
	"fmt"
	"testing"
)

// PRT-04: IPv6-Clients werden je /64 gezählt — ein Anschluss mit einem ganzen /64
// darf nicht mit jeder neuen Quelladresse einen frischen, vollen Bucket bekommen.
func TestRateLimitKeyGroupsIPv6By64(t *testing.T) {
	cases := map[string]string{
		"203.0.113.7":                  "203.0.113.7",
		"::ffff:203.0.113.7":           "203.0.113.7",
		"2001:db8:1:2:aaaa::1":         "2001:db8:1:2::/64",
		"2001:db8:1:2:ffff:ffff:0:9":   "2001:db8:1:2::/64",
		"2001:db8:1:3::1":              "2001:db8:1:3::/64",
		"fe80::1%eth0":                 "fe80::/64",
		"not-an-ip":                    "not-an-ip",
		"2001:0db8:0001:0002:0:0:0:ff": "2001:db8:1:2::/64",
	}
	for in, want := range cases {
		if got := rateLimitKey(in); got != want {
			t.Errorf("rateLimitKey(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestIPLimiterRotatingIPv6SharesBudget(t *testing.T) {
	l := newIPLimiter(3)
	allowed := 0
	for i := 0; i < 10; i++ {
		if l.allow(fmt.Sprintf("2001:db8:0:1::%x", i+1)) {
			allowed++
		}
	}
	if allowed != 3 {
		t.Errorf("rotating addresses inside one /64 got %d requests through, want 3", allowed)
	}
	if len(l.buckets) != 1 {
		t.Errorf("one /64 created %d buckets, want 1", len(l.buckets))
	}
}

// Die Tabelle ist gedeckelt: ist sie voll, teilen sich alle NEUEN Schlüssel einen
// Überlauf-Bucket, bestehende Clients behalten ihren eigenen.
func TestIPLimiterBucketMapIsCapped(t *testing.T) {
	l := newIPLimiter(5)
	for i := 0; i < maxRateBuckets; i++ {
		l.buckets[fmt.Sprintf("k%d", i)] = &bucket{tokens: 5}
	}
	// New clients from one /16 share one coarse bucket and drain it together …
	for i := 0; i < 20; i++ {
		l.allow(fmt.Sprintf("10.0.%d.1", i))
	}
	if b := l.buckets[aggregateKey("10.0.0.1")]; b == nil || b.tokens >= 1 {
		t.Errorf("new keys of one network must share and drain their aggregate bucket: %+v", b)
	}
	// … while a new client from another network is not throttled by that flood.
	if !l.allow("192.168.1.1") {
		t.Error("a flood from one network throttled a new client from another")
	}
	if !l.allow("k1") {
		t.Error("an existing client lost its own bucket when the map filled up")
	}
	// The aggregate buckets are capped as well; beyond that, the global bucket.
	for i := 0; l.aggBuckets < maxAggregateBuckets; i++ {
		l.allow(fmt.Sprintf("2001:db8:%x::1", i))
	}
	l.allow("2001:db9::1")
	if _, ok := l.buckets[overflowBucketKey]; !ok {
		t.Error("with the aggregate buckets exhausted, new keys must fall back to the overflow bucket")
	}
	if n := len(l.buckets); n > maxRateBuckets+maxAggregateBuckets+1 {
		t.Fatalf("bucket map grew past the cap: %d", n)
	}
}
