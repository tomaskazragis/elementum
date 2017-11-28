package memory

type key int

func (me key) Before(other policyItemKey) bool {
	return me < other.(key)
}
