package api

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDeployScriptsDoNotPinUnsignedConsensusForever(t *testing.T) {
	root := repoRootForDeployGuard(t)
	files := []string{
		"QSDM/deploy/install-ubuntu-vps.sh",
		"QSDM/deploy/bring-up-validator.sh",
	}

	for _, rel := range files {
		raw, err := os.ReadFile(filepath.Join(root, rel)) // #nosec G304 -- git-tracked paths
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		s := string(raw)
		if strings.Contains(s, "require_signed_votes = false\nsigned_message_activation_height = 0") {
			t.Fatalf("%s pins signed-consensus enforcement off forever; production bring-up must be env/flag driven", rel)
		}
		if !strings.Contains(s, "QSDM_SIGNED_MESSAGE_ACTIVATION_HEIGHT") &&
			!strings.Contains(s, "--signed-message-activation-height") {
			t.Fatalf("%s does not expose a coordinated signed-message activation height", rel)
		}
	}
}
