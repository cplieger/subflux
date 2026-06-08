package server

// auth_delegate.go provides thin delegation methods from *Server to
// *authhandlers.Handler. These exist solely for test compatibility:
// the auth_handlers_test.go and auth_integration_test.go files call
// s.handleLogin etc. directly. The production route registrations in
// routes.go reference s.authH.HandleX directly.

import "net/http"

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request)  { s.authH.HandleLogin(w, r) }
func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) { s.authH.HandleLogout(w, r) }
func (s *Server) handleSetupStatus(w http.ResponseWriter, r *http.Request) {
	s.authH.HandleSetupStatus(w, r)
}

func (s *Server) handleSetupCreate(w http.ResponseWriter, r *http.Request) {
	s.authH.HandleSetupCreate(w, r)
}
func (s *Server) handleAuthMe(w http.ResponseWriter, r *http.Request) { s.authH.HandleAuthMe(w, r) }
func (s *Server) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	s.authH.HandleChangePassword(w, r)
}

func (s *Server) handleListPasskeys(w http.ResponseWriter, r *http.Request) {
	s.authH.HandleListPasskeys(w, r)
}

func (s *Server) handleDeletePasskey(w http.ResponseWriter, r *http.Request) {
	s.authH.HandleDeletePasskey(w, r)
}

func (s *Server) handleRenamePasskey(w http.ResponseWriter, r *http.Request) {
	s.authH.HandleRenamePasskey(w, r)
}

func (s *Server) handleListUsers(w http.ResponseWriter, r *http.Request) {
	s.authH.HandleListUsers(w, r)
}

func (s *Server) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	s.authH.HandleCreateUser(w, r)
}

func (s *Server) handleDeleteUser(w http.ResponseWriter, r *http.Request) {
	s.authH.HandleDeleteUser(w, r)
}

func (s *Server) handleListAPIKeys(w http.ResponseWriter, r *http.Request) {
	s.authH.HandleListAPIKeys(w, r)
}

func (s *Server) handleGenerateAPIKey(w http.ResponseWriter, r *http.Request) {
	s.authH.HandleGenerateAPIKey(w, r)
}

func (s *Server) handleRevokeAPIKey(w http.ResponseWriter, r *http.Request) {
	s.authH.HandleRevokeAPIKey(w, r)
}
