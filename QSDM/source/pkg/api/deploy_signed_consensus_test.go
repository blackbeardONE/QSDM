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

func TestDeployScriptsSetCoordinatedConsensusForkHeights(t *testing.T) {
	root := repoRootForDeployGuard(t)
	files := []struct {
		rel     string
		exposed []string
		written []string
	}{
		{
			rel: "QSDM/deploy/install-ubuntu-vps.sh",
			exposed: []string{
				"QSDM_TASK_ACTION_SIGNATURE_ACTIVATION_HEIGHT",
				"QSDM_TX_CONTENT_ROOT_ACTIVATION_HEIGHT",
				"QSDM_ENROLLMENT_STATE_ROOT_ACTIVATION_HEIGHT",
			},
			written: []string{
				"task_action_signature_activation_height = ${QSDM_TASK_ACTION_SIGNATURE_ACTIVATION_HEIGHT_VALUE}",
				"tx_content_root_activation_height = ${QSDM_TX_CONTENT_ROOT_ACTIVATION_HEIGHT_VALUE}",
				"enrollment_state_root_activation_height = ${QSDM_ENROLLMENT_STATE_ROOT_ACTIVATION_HEIGHT_VALUE}",
			},
		},
		{
			rel: "QSDM/deploy/bring-up-validator.sh",
			exposed: []string{
				"QSDM_TASK_ACTION_SIGNATURE_ACTIVATION_HEIGHT",
				"QSDM_TX_CONTENT_ROOT_ACTIVATION_HEIGHT",
				"QSDM_ENROLLMENT_STATE_ROOT_ACTIVATION_HEIGHT",
				"--task-action-signature-activation-height",
				"--tx-content-root-activation-height",
				"--enrollment-state-root-activation-height",
			},
			written: []string{
				"task_action_signature_activation_height = ${TASK_ACTION_SIGNATURE_ACTIVATION_HEIGHT}",
				"tx_content_root_activation_height = ${TX_CONTENT_ROOT_ACTIVATION_HEIGHT}",
				"enrollment_state_root_activation_height = ${ENROLLMENT_STATE_ROOT_ACTIVATION_HEIGHT}",
			},
		},
	}

	for _, file := range files {
		raw, err := os.ReadFile(filepath.Join(root, file.rel)) // #nosec G304 -- git-tracked paths
		if err != nil {
			t.Fatalf("read %s: %v", file.rel, err)
		}
		s := string(raw)
		for _, want := range file.exposed {
			if !strings.Contains(s, want) {
				t.Fatalf("%s does not expose %s", file.rel, want)
			}
		}
		for _, want := range file.written {
			if !strings.Contains(s, want) {
				t.Fatalf("%s does not write %s", file.rel, want)
			}
		}
	}
}
