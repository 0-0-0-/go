package resource

import (
	"github.com/kinecosystem/go/services/horizon/internal/db2/core"
)

func (this *AccountFlags) Populate(row core.Account) {
	this.AuthRequired = row.IsAuthRequired()
	this.AuthRevocable = row.IsAuthRevocable()
}
