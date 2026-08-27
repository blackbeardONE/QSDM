package chain

import (
	"os"
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/mining/slashing"
)

// TestMain enables the attestation-attribution gate for this package's tests.
//
// forgedattest and doublemining are closed behind that gate in production,
// because nvidia-hmac-v1 authenticates with a symmetric key that
// EnrollmentRecord stores as public chain state: a valid MAC can be computed
// by any chain-state reader, and an invalid one manufactured against any
// victim. Neither offence is therefore attributable, and slashing on
// unattributable evidence is a bond-theft primitive. See
// pkg/mining/slashing/attribution.go.
//
// The slash end-to-end tests here exercise the PIPELINE past that gate -- bond
// drain, slasher reward, replay fingerprinting -- which stays worth testing
// for whenever attestation becomes asymmetric and the gate legitimately opens.
//
// It is set once for the package rather than per test, deliberately: the flag
// is a process-global and several of those tests call t.Parallel(), so a
// per-test set/restore would let one test's cleanup close the gate underneath
// another and produce flakes.
//
// The gate's own behaviour is asserted where it can be exercised serially, in
// pkg/mining/slashing/{forgedattest,doublemining}: TestVerify_GateClosedByDefault
// and TestVerify_GateClosedBeatsOtherRejections. Nothing here changes what a
// node does -- SetHMACAttestationAttributable is never called outside tests.
func TestMain(m *testing.M) {
	slashing.SetHMACAttestationAttributable(true)
	code := m.Run()
	slashing.SetHMACAttestationAttributable(false)
	os.Exit(code)
}
