package nps

import "umpp/control-plane/internal/models"

func accessControlValue() models.AccessControl {
	return models.AccessControl{
		IPWhitelist: []string{"1.2.3.4/32"},
		BasicAuth:   models.BasicAuth{Enabled: true, Username: "u", PasswordHash: "$2a$10$hash"},
		RequireJWT:  false,
	}
}
