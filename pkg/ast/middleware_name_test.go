package ast

import "testing"

func TestMiddlewareName(t *testing.T) {
	cases := map[string]string{
		"Auth(jwtSecret)":        "Auth",
		"users.AuthMiddleware()": "AuthMiddleware",
		"RateLimit()":            "RateLimit",
		`RequireRole("admin")`:   "RequireRole",
	}
	for in, want := range cases {
		if got := MiddlewareName(in); got != want {
			t.Errorf("MiddlewareName(%q) = %q, want %q", in, got, want)
		}
	}
}
