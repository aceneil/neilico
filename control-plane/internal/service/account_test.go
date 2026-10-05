package service

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"neilico/control-plane/internal/auth"
	"neilico/control-plane/internal/config"
	"neilico/control-plane/internal/db"
	"neilico/control-plane/internal/models"
)

func newAccountTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	handle, err := db.Open(config.Database{
		Driver: "sqlite",
		DSN:    "file:" + uuid.NewString() + "?mode=memory&cache=shared",
	}, "error")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(handle); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		sqlDB, dbErr := handle.DB()
		if dbErr == nil {
			_ = sqlDB.Close()
		}
	})
	return handle
}

func TestRegisterFirstAdminOnlyOnce(t *testing.T) {
	handle := newAccountTestDB(t)
	users := NewUserService(handle)
	ctx := context.Background()

	if count, err := users.UserCount(ctx); err != nil || count != 0 {
		t.Fatalf("fresh user count = %d err = %v", count, err)
	}
	admin, err := users.RegisterFirstAdmin(ctx, RegisterInput{
		Email: "First-Admin@Example.test", Password: "Correct-Horse-Battery-9!",
	}, "default")
	if err != nil {
		t.Fatalf("RegisterFirstAdmin() error = %v", err)
	}
	if admin.Role != auth.RolePlatformAdmin || admin.Status != "active" {
		t.Fatalf("unexpected admin: %#v", admin)
	}
	// 邮箱规范化 + 密码只存哈希。
	if admin.Email != "first-admin@example.test" {
		t.Fatalf("email not normalized: %q", admin.Email)
	}
	if admin.PasswordHash == "Correct-Horse-Battery-9!" || !auth.CheckPassword(admin.PasswordHash, "Correct-Horse-Battery-9!") {
		t.Fatal("password was not stored as a bcrypt hash")
	}
	// 已有账号后再注册 → 冲突。
	if _, err := users.RegisterFirstAdmin(ctx, RegisterInput{
		Email: "second@example.test", Password: "Correct-Horse-Battery-9!",
	}, "default"); !errors.Is(err, ErrConflict) {
		t.Fatalf("second registration error = %v, want conflict", err)
	}
	// 弱密码在账号存在时也会先被冲突拦截，但空库时应报 invalid input。
	empty := NewUserService(newAccountTestDB(t))
	if _, err := empty.RegisterFirstAdmin(ctx, RegisterInput{
		Email: "weak@example.test", Password: "short",
	}, "default"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("weak password error = %v, want invalid input", err)
	}
}

func TestChangeEmailValidatesPasswordAndUniqueness(t *testing.T) {
	handle := newAccountTestDB(t)
	users := NewUserService(handle)
	ctx := context.Background()
	tenantID := uuid.New()
	if err := handle.Create(&models.Tenant{ID: tenantID, Name: "acct"}).Error; err != nil {
		t.Fatal(err)
	}
	admin, err := users.Create(ctx, UserInput{Email: "owner@example.test", Password: "owner-password", Role: auth.RolePlatformAdmin}, tenantID, true)
	if err != nil {
		t.Fatal(err)
	}
	other, err := users.Create(ctx, UserInput{Email: "occupied@example.test", Password: "other-password", Role: auth.RoleOps}, tenantID, true)
	if err != nil {
		t.Fatal(err)
	}

	// 当前密码错误 → forbidden。
	if _, err := users.ChangeEmail(ctx, admin.ID, "wrong-password", "new@example.test"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("wrong password error = %v, want forbidden", err)
	}
	// 目标邮箱被占用 → conflict。
	if _, err := users.ChangeEmail(ctx, admin.ID, "owner-password", other.Email); !errors.Is(err, ErrConflict) {
		t.Fatalf("email conflict error = %v, want conflict", err)
	}
	// 正常改名。
	updated, err := users.ChangeEmail(ctx, admin.ID, "owner-password", "New-Owner@Example.test")
	if err != nil {
		t.Fatalf("ChangeEmail() error = %v", err)
	}
	if updated.Email != "new-owner@example.test" {
		t.Fatalf("email = %q, want normalized new-owner@example.test", updated.Email)
	}
}

func TestRotatePasswordInvalidatesOldPasswordAndBumpsTokenVersion(t *testing.T) {
	handle := newAccountTestDB(t)
	users := NewUserService(handle)
	ctx := context.Background()
	tenantID := uuid.New()
	if err := handle.Create(&models.Tenant{ID: tenantID, Name: "rotate"}).Error; err != nil {
		t.Fatal(err)
	}
	admin, err := users.Create(ctx, UserInput{Email: "rotate@example.test", Password: "Original-Password-1!", Role: auth.RolePlatformAdmin}, tenantID, true)
	if err != nil {
		t.Fatal(err)
	}
	before, err := users.Get(ctx, admin.ID, nil)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := users.RotatePassword(ctx, admin.ID, "wrong-password", "New-Rotated-Password-2!"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("wrong current password error = %v, want forbidden", err)
	}
	if _, err := users.RotatePassword(ctx, admin.ID, "Original-Password-1!", "tooshort"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("weak new password error = %v, want invalid input", err)
	}

	updated, err := users.RotatePassword(ctx, admin.ID, "Original-Password-1!", "New-Rotated-Password-2!")
	if err != nil {
		t.Fatalf("RotatePassword() error = %v", err)
	}
	if updated.TokenVersion != before.TokenVersion+1 {
		t.Fatalf("token_version = %d, want %d", updated.TokenVersion, before.TokenVersion+1)
	}
	if auth.CheckPassword(updated.PasswordHash, "Original-Password-1!") {
		t.Fatal("old password still valid after rotation")
	}
	if !auth.CheckPassword(updated.PasswordHash, "New-Rotated-Password-2!") {
		t.Fatal("new password not valid after rotation")
	}
}

func TestValidatePasswordStrength(t *testing.T) {
	cases := []struct {
		name     string
		password string
		wantOK   bool
	}{
		{"too short", "Ab1!Ab1!Ab1!Ab1", false},
		{"long but only two classes", "abcdefghijklmnopqrst", false},
		{"long mixed three classes", "Abcdefghijklmnop12", true},
		{"all four classes", "Correct-Horse-Battery-9!", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidatePasswordStrength(tc.password)
			if tc.wantOK && err != nil {
				t.Fatalf("ValidatePasswordStrength(%q) = %v, want nil", tc.password, err)
			}
			if !tc.wantOK && !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("ValidatePasswordStrength(%q) = %v, want invalid input", tc.password, err)
			}
		})
	}
}
