package statedb

import (
	"github.com/cplieger/access"
	"github.com/cplieger/access/account"
)

var (
	_ account.Store             = (*AccountStore)(nil)
	_ account.Tx                = (*accountTx)(nil)
	_ access.AuthenticatorStore = (*AccountStore)(nil)
)
