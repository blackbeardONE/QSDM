package enrollment

import (
	"math"
	"sync/atomic"
)

// operatorPublicKeyRetentionHeight gates the consensus-visible addition of the
// enrollment signer public key to EnrollmentRecord. The default is inert so a
// binary upgrade does not change historical replay or active chain state until
// the network deliberately schedules the migration.
var operatorPublicKeyRetentionHeight atomic.Uint64

func init() {
	operatorPublicKeyRetentionHeight.Store(math.MaxUint64)
}

// OperatorPublicKeyRetentionHeight returns the height at which signed
// enrollment records start retaining the ML-DSA public key from their envelope.
func OperatorPublicKeyRetentionHeight() uint64 {
	return operatorPublicKeyRetentionHeight.Load()
}

// SetOperatorPublicKeyRetentionHeight pins the height at which enrollment
// records retain signed-envelope public keys. Call during chain startup or in
// tests only; changing it at runtime would be a consensus bug.
func SetOperatorPublicKeyRetentionHeight(h uint64) {
	operatorPublicKeyRetentionHeight.Store(h)
}

// RetainOperatorPublicKey reports whether a signed enrollment applied at height
// h should write its operator public key into consensus state.
func RetainOperatorPublicKey(h uint64) bool {
	return h >= OperatorPublicKeyRetentionHeight()
}
