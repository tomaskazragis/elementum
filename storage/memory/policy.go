package memory

import "time"

type policyItemKey interface {
	Before(policyItemKey) bool
}

// Policy ...
type Policy interface {
	Choose() policyItemKey
	Used(k policyItemKey, at time.Time)
	Forget(k policyItemKey)
	NumItems() int
}
