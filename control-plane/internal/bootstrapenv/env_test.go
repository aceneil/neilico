package bootstrapenv

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readEnv(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read env file: %v", err)
	}
	return string(data)
}

func TestUpdateReplacesOnlyTargetKeysAndPreservesRest(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "neilico.env")
	original := strings.Join([]string{
		"POSTGRES_DB=neilico",
		"POSTGRES_PASSWORD=db-secret-should-not-change",
		"# 注释行原样保留",
		"NEILICO_AUTH_JWT_SECRET=jwt-secret-should-not-change",
		"NEILICO_BOOTSTRAP_ADMIN_EMAIL=old-admin@example.test",
		"NEILICO_BOOTSTRAP_ADMIN_PASSWORD=old-admin-password",
		"NEILICO_LOG_LEVEL=info",
		"",
	}, "\n")
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := Update(path, map[string]string{
		"NEILICO_BOOTSTRAP_ADMIN_EMAIL":    "new-admin@example.test",
		"NEILICO_BOOTSTRAP_ADMIN_PASSWORD": "brand-new-strong-password-123!",
	}); err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	got := readEnv(t, path)
	if !strings.Contains(got, "NEILICO_BOOTSTRAP_ADMIN_EMAIL=new-admin@example.test\n") {
		t.Fatalf("email key not replaced:\n%s", got)
	}
	if !strings.Contains(got, "NEILICO_BOOTSTRAP_ADMIN_PASSWORD=brand-new-strong-password-123!\n") {
		t.Fatalf("password key not replaced:\n%s", got)
	}
	// 旧值必须消失。
	if strings.Contains(got, "old-admin@example.test") || strings.Contains(got, "old-admin-password") {
		t.Fatalf("old values lingering:\n%s", got)
	}
	// 其它行逐字保留。
	for _, line := range []string{
		"POSTGRES_DB=neilico",
		"POSTGRES_PASSWORD=db-secret-should-not-change",
		"# 注释行原样保留",
		"NEILICO_AUTH_JWT_SECRET=jwt-secret-should-not-change",
		"NEILICO_LOG_LEVEL=info",
	} {
		if !strings.Contains(got, line+"\n") {
			t.Fatalf("expected line %q preserved, got:\n%s", line, got)
		}
	}
	// 行数不变（只替换、不新增）。
	if strings.Count(got, "\n") != strings.Count(original, "\n") {
		t.Fatalf("line count changed: original=%d got=%d\n%s",
			strings.Count(original, "\n"), strings.Count(got, "\n"), got)
	}
	// 权限固定 0600。
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("mode = %o, want 600", perm)
	}
	// 目录里没有残留临时文件。
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "neilico.env" {
		t.Fatalf("unexpected leftover files: %v", entries)
	}
}

func TestUpdateAppendsMissingKeys(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "neilico.env")
	if err := os.WriteFile(path, []byte("POSTGRES_DB=neilico\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Update(path, map[string]string{
		"NEILICO_BOOTSTRAP_ADMIN_PASSWORD": "first-time-strong-password!",
	}); err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	got := readEnv(t, path)
	if !strings.Contains(got, "POSTGRES_DB=neilico\n") {
		t.Fatalf("existing line lost:\n%s", got)
	}
	if !strings.Contains(got, "NEILICO_BOOTSTRAP_ADMIN_PASSWORD=first-time-strong-password!\n") {
		t.Fatalf("missing key was not appended:\n%s", got)
	}
}

func TestUpdateCreatesFileWhenAbsent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bootstrap.env")
	if err := Update(path, map[string]string{"NEILICO_BOOTSTRAP_ADMIN_PASSWORD": "created-password!"}); err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if got := readEnv(t, path); !strings.Contains(got, "NEILICO_BOOTSTRAP_ADMIN_PASSWORD=created-password!\n") {
		t.Fatalf("file not created with key:\n%s", got)
	}
}

func TestUpdateRejectsEmptyPath(t *testing.T) {
	if err := Update("", map[string]string{"A": "b"}); err == nil {
		t.Fatal("expected error for empty path")
	}
}
