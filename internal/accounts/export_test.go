package accounts

import "github.com/cplieger/access/account"

// AccountOf is s's composed account service, for the external tests that
// drive the library's own removals beside a settings service, which imports
// this package.
func AccountOf(s *Service) *account.Service { return s.account }
