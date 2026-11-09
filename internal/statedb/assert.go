package statedb

import (
	"github.com/cplieger/access"
	"github.com/cplieger/access/account"
)

// No call site in this module hands an AccountStore to access or account
// before platform-auth wires them, so the compiler checks the SPI here.
var (
	_ account.Store             = (*AccountStore)(nil)
	_ account.Tx                = (*accountTx)(nil)
	_ access.AuthenticatorStore = (*AccountStore)(nil)
)
