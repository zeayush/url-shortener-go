package config

import (
	"os"
	"testing"
)

// REDIS_ADDR distinguishes "not configured" from "configured but down". The
// difference is seconds per request on a deployment with no Redis alongside
// it, so it is worth pinning.
func TestRedisAddrDistinguishesUnsetFromDisabled(t *testing.T) {
	cases := []struct {
		name string
		set  bool
		val  string
		want string
	}{
		{"unset keeps dev default", false, "", "localhost:6379"},
		{"empty disables", true, "", ""},
		{"off disables", true, "off", ""},
		{"none disables", true, "none", ""},
		{"disabled disables", true, "disabled", ""},
		{"mixed case disables", true, "OFF", ""},
		{"whitespace disables", true, "  ", ""},
		{"real address kept", true, "redis-12345.upstash.io:6379", "redis-12345.upstash.io:6379"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.set {
				t.Setenv("REDIS_ADDR", tc.val)
			} else {
				// t.Setenv then unset is the only way to guarantee absence
				// regardless of the caller's environment.
				t.Setenv("REDIS_ADDR", "")
				if err := unsetEnv("REDIS_ADDR"); err != nil {
					t.Fatalf("unset: %v", err)
				}
			}
			if got := redisAddr(); got != tc.want {
				t.Errorf("redisAddr() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestRedisEnabled(t *testing.T) {
	if (&Config{RedisAddr: ""}).RedisEnabled() {
		t.Error("empty addr should report disabled")
	}
	if !(&Config{RedisAddr: "host:6379"}).RedisEnabled() {
		t.Error("real addr should report enabled")
	}
}

func unsetEnv(k string) error { return os.Unsetenv(k) }
