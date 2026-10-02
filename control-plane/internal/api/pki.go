package api

import (
	"errors"
	"net/http"

	"gorm.io/gorm"

	"neilico/control-plane/internal/auth"
	"neilico/control-plane/internal/middleware"
	"neilico/control-plane/internal/models"
	"neilico/control-plane/internal/service/pki"
)

func (s *Server) handlePKICA(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.methodNotAllowed(w, http.MethodGet)
		return
	}
	if _, err := s.pki.EnsureCA(); err != nil {
		s.pkiError(w, err)
		return
	}
	caPEM, err := s.pki.CAPEM()
	if err != nil {
		s.pkiError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ca_cert_pem": caPEM})
}

func (s *Server) handlePKICARotate(w http.ResponseWriter, r *http.Request) {
	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "valid access token required")
		return
	}
	if r.Method != http.MethodPost {
		s.methodNotAllowed(w, http.MethodPost)
		return
	}
	if principal.Role != auth.RolePlatformAdmin {
		writeError(w, http.StatusForbidden, "forbidden", "platform administrator role required")
		return
	}
	item, err := s.pki.RotateCA()
	if err != nil {
		s.pkiError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"id": item.ID, "name": item.Name,
		"not_before": item.NotBefore, "not_after": item.NotAfter,
		"created_at": item.CreatedAt,
	})
}

func (s *Server) handleNodeMTLS(w http.ResponseWriter, r *http.Request) {
	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "valid access token required")
		return
	}
	nodeID, ok := s.pathID(w, r.PathValue("id"), "node ID")
	if !ok {
		return
	}
	scope := s.userScope(principal)
	var node models.Node
	if err := s.db.WithContext(r.Context()).Where("id = ?", nodeID).First(&node).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "node not found")
			return
		}
		s.internalError(w, err)
		return
	}
	if scope != nil && *scope != node.TenantID {
		writeError(w, http.StatusNotFound, "not_found", "node not found")
		return
	}
	switch r.Method {
	case http.MethodGet:
		metadata, err := s.pki.NodeCertificateMetadata(r.Context(), nodeID)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "node client certificate has not been issued")
			return
		}
		if err != nil {
			s.pkiError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, metadata)
	case http.MethodPost:
		if !roleAllowed(r.Context(), principal, auth.RolePlatformAdmin, auth.RoleTenantAdmin, auth.RoleOps) {
			writeError(w, http.StatusForbidden, "forbidden", "insufficient role")
			return
		}
		issued, err := s.pki.IssueNodeCert(node.ID, node.Name)
		if err != nil {
			s.pkiError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{
			"client_cert_pem": issued.CertPEM,
			"client_key_pem":  issued.KeyPEM,
			"certificate":     issued.Metadata,
		})
	default:
		s.methodNotAllowed(w, http.MethodGet, http.MethodPost)
	}
}

func (s *Server) pkiError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, pki.ErrDisabled):
		writeError(w, http.StatusServiceUnavailable, "pki_disabled", err.Error())
	case errors.Is(err, gorm.ErrRecordNotFound):
		writeError(w, http.StatusNotFound, "not_found", "PKI resource not found")
	default:
		s.internalError(w, err)
	}
}
