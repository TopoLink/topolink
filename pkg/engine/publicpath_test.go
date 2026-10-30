package engine

import "testing"

func TestIsPublicPath(t *testing.T) {
	public := []string{
		"/health", "/healthz", "/ping", "/metrics", "/ready", "/live",
		"/version", "/login", "/signin", "/register", "/oauth/callback",
		"/webhook/github", "/swagger/index.html", "/favicon.ico",
	}
	for _, p := range public {
		if !isPublicPath(p) {
			t.Errorf("isPublicPath(%q) = false, want true", p)
		}
	}

	// 首段必须精确匹配，避免把敏感路径误判为公开。
	private := []string{
		"/public", "/health-records", "/login-history", "/orders",
		"/api/health", "/admin/metrics-users",
	}
	for _, p := range private {
		if isPublicPath(p) {
			t.Errorf("isPublicPath(%q) = true, want false", p)
		}
	}
}
