package memory

import "time"

type policyItemKey interface {
	Before(policyItemKey) bool
	Less(policyItemKey) bool
}

// Policy ...
type Policy interface {
	GetCompleted() policyItemKey
	Choose() policyItemKey
	Used(k policyItemKey, at time.Time, completed bool)
	Forget(k policyItemKey)
	NumItems() int
}
