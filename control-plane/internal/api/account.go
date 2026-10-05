package api

import (
	"errors"
	"net/http"
	"strings"

	"neilico/control-plane/internal/auth"
	"neilico/control-plane/internal/bootstrapenv"
	"neilico/control-plane/internal/middleware"
	"neilico/control-plane/internal/models"
	"neilico/control-plane/internal/service"
)

// passwordPolicyView 是暴露给前端的密码强度说明（与 service 层校验保持一致）。
func passwordPolicyView() map[string]any {
	return map[string]any{
		"min_length":  service.MinAdminPasswordLength,
		"max_length":  service.MaxAdminPasswordLength,
		"min_classes": service.RequiredCharacterClasses,
		"description": "至少 16 个字符，且包含大写字母、小写字母、数字、符号中的至少三类",
	}
}

// accountView 是自助账号信息的对外视图（不含任何口令字段）。
func accountView(user models.User) map[string]any {
	return map[string]any{
		"id":         user.ID,
		"tenant_id":  user.TenantID,
		"email":      user.Email,
		"role":       user.Role,
		"status":     user.Status,
		"created_at": user.CreatedAt,
	}
}

// registerAccount 注册「初始化状态」与「自助账号」相关路由。
//   - 公开：GET  /api/v1/setup/status、POST /api/v1/setup/register
//   - 登录后：GET  /api/v1/account、PUT /api/v1/account/email、POST /api/v1/account/password/rotate
func (s *Server) registerAccount(mux *http.ServeMux) {
	mux.HandleFunc("/api/v1/setup/status", s.handleSetupStatus)
	mux.HandleFunc("/api/v1/setup/register", s.handleSetupRegister)
	mux.Handle("/api/v1/account", s.authed(http.HandlerFunc(s.handleAccount)))
	mux.Handle("/api/v1/account/email", s.authed(http.HandlerFunc(s.handleAccountEmail)))
	mux.Handle("/api/v1/account/password/rotate", s.authed(http.HandlerFunc(s.handleAccountPasswordRotate)))
}

func (s *Server) handleSetupStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.methodNotAllowed(w, http.MethodGet)
		return
	}
	count, err := s.users.UserCount(r.Context())
	if err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"initialized":       count > 0,
		"registration_open": count == 0,
		"password_policy":   passwordPolicyView(),
	})
}

func (s *Server) handleSetupRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.methodNotAllowed(w, http.MethodPost)
		return
	}
	var input service.RegisterInput
	if !s.decodeRequest(w, r, &input) {
		return
	}
	user, err := s.users.RegisterFirstAdmin(r.Context(), input, s.defaultTenant)
	if err != nil {
		if errors.Is(err, service.ErrConflict) {
			middleware.SetAuditAction(r.Context(), "setup.register", "/api/v1/setup/register",
				map[string]any{"result": "already_initialized"})
			writeError(w, http.StatusConflict, "already_initialized",
				"system already has an account; sign in instead")
			return
		}
		s.serviceError(w, err)
		return
	}
	response, err := s.tokenResponse(user)
	if err != nil {
		s.internalError(w, err)
		return
	}
	middleware.SetAuditAction(r.Context(), "setup.register", "/api/v1/setup/register",
		map[string]any{"user_id": user.ID, "email": user.Email})
	writeJSON(w, http.StatusCreated, response)
}

// requireUserSession 只允许「用户会话（JWT）」访问自助账号接口；
// API Token 不能代替本人改邮箱/改密码。
func (s *Server) requireUserSession(w http.ResponseWriter, r *http.Request) (middleware.Principal, bool) {
	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "valid access token required")
		return middleware.Principal{}, false
	}
	if principal.AuthMethod != auth.AuthMethodJWT || principal.UserID == [16]byte{} {
		writeError(w, http.StatusForbidden, "forbidden", "self-service account management requires a user session")
		return middleware.Principal{}, false
	}
	return principal, true
}

func (s *Server) handleAccount(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requireUserSession(w, r)
	if !ok {
		return
	}
	if r.Method != http.MethodGet {
		s.methodNotAllowed(w, http.MethodGet)
		return
	}
	user, err := s.users.Get(r.Context(), principal.UserID, nil)
	if err != nil {
		s.serviceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"account":         accountView(user),
		"password_policy": passwordPolicyView(),
	})
}

func (s *Server) handleAccountEmail(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requireUserSession(w, r)
	if !ok {
		return
	}
	if r.Method != http.MethodPut {
		s.methodNotAllowed(w, http.MethodPut)
		return
	}
	var input struct {
		CurrentPassword string `json:"current_password"`
		Email           string `json:"email"`
	}
	if !s.decodeRequest(w, r, &input) {
		return
	}
	if strings.TrimSpace(input.CurrentPassword) == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "current_password is required")
		return
	}
	user, err := s.users.ChangeEmail(r.Context(), principal.UserID, input.CurrentPassword, input.Email)
	if err != nil {
		s.accountError(w, err)
		return
	}
	envUpdated, envErr := s.writeBootstrapEnv(map[string]string{
		"NEILICO_BOOTSTRAP_ADMIN_EMAIL": user.Email,
	})
	if envErr != nil {
		s.logger.Warn("failed to update bootstrap env email", "error", envErr)
	}
	middleware.SetAuditAction(r.Context(), "account.email.change", "/api/v1/account/email",
		map[string]any{"user_id": user.ID, "email": user.Email, "env_file_updated": envUpdated})
	writeJSON(w, http.StatusOK, map[string]any{
		"account":          accountView(user),
		"env_file_updated": envUpdated,
	})
}

func (s *Server) handleAccountPasswordRotate(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requireUserSession(w, r)
	if !ok {
		return
	}
	if r.Method != http.MethodPost {
		s.methodNotAllowed(w, http.MethodPost)
		return
	}
	var input struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
	}
	if !s.decodeRequest(w, r, &input) {
		return
	}
	if strings.TrimSpace(input.CurrentPassword) == "" || input.NewPassword == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "current_password and new_password are required")
		return
	}
	user, err := s.users.RotatePassword(r.Context(), principal.UserID, input.CurrentPassword, input.NewPassword)
	if err != nil {
		s.accountError(w, err)
		return
	}
	// 回写 env：新密码 + （若改过）邮箱，保持 show-admin-password.sh 读同一键可用。
	envUpdated, envErr := s.writeBootstrapEnv(map[string]string{
		"NEILICO_BOOTSTRAP_ADMIN_PASSWORD": input.NewPassword,
		"NEILICO_BOOTSTRAP_ADMIN_EMAIL":    user.Email,
	})
	if envErr != nil {
		s.logger.Warn("failed to update bootstrap env after password rotation", "error", envErr)
	}
	// 让本人当前会话继续可用：签发带新 token_version 的凭据。
	response, tokenErr := s.tokenResponse(user)
	if tokenErr != nil {
		s.internalError(w, tokenErr)
		return
	}
	response["env_file_updated"] = envUpdated
	response["notice"] = "密码已更新；此前的 refresh token 已失效，本次响应返回了新的会话凭据。"
	middleware.SetAuditAction(r.Context(), "account.password.rotate", "/api/v1/account/password/rotate",
		map[string]any{"user_id": user.ID, "email": user.Email, "env_file_updated": envUpdated})
	writeJSON(w, http.StatusOK, response)
}

// accountError 把自助账号错误的语义映射成明确状态码：
// 当前密码错误 → 403 invalid_password；邮箱占用 → 409；其余走通用映射。
func (s *Server) accountError(w http.ResponseWriter, err error) {
	if errors.Is(err, service.ErrForbidden) {
		writeError(w, http.StatusForbidden, "invalid_password", "current password is incorrect")
		return
	}
	s.serviceError(w, err)
}

// writeBootstrapEnv 原子回写 bootstrap env。未配置路径时按「未启用」处理（返回 false, nil）。
// 任何情况下都不记录写入的值。
func (s *Server) writeBootstrapEnv(updates map[string]string) (bool, error) {
	path := strings.TrimSpace(s.bootstrapEnvFile)
	if path == "" {
		return false, nil
	}
	if err := bootstrapenv.Update(path, updates); err != nil {
		return false, err
	}
	return true, nil
}
