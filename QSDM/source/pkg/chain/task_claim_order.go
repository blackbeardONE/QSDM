package chain

import (
	"sort"
	"strconv"
)

// claimRoundKeys returns the round keys of submissions in ascending numeric
// order (keys that are not numbers, which no action writes, sort last by text).
//
// A claim adds up the rewards of its rounds. Go map order is random, and a
// floating-point sum in a different order can differ in its last bit (0.1+0.2+0.3
// is 0.6000000000000001 or 0.6), so both the claimable amount and the applied
// claim walk the rounds in this fixed order: every computation of the same claim
// gives the same amount.
func claimRoundKeys(submissions map[string]map[string]TaskSubmissionState) []string {
	keys := make([]string, 0, len(submissions))
	for k := range submissions {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		a, errA := strconv.ParseUint(keys[i], 10, 64)
		b, errB := strconv.ParseUint(keys[j], 10, 64)
		switch {
		case errA == nil && errB == nil:
			return a < b
		case errA == nil:
			return true
		case errB == nil:
			return false
		default:
			return keys[i] < keys[j]
		}
	})
	return keys
}
