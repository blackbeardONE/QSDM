package api

import (
	"net/http"
	"sort"
	"sync"

	"github.com/blackbeardONE/QSDM/pkg/chain"
)

type validatorSetProvider interface {
	RegisteredAddresses() []string
	ActiveValidators() []chain.Validator
	GetValidator(address string) (*chain.Validator, bool)
	CurrentEpoch() uint64
}

type validatorSetProviderHolder struct {
	mu       sync.RWMutex
	provider validatorSetProvider
}

var publicValidatorSetProvider = &validatorSetProviderHolder{}

// SetValidatorSetProvider installs the committed validator-set projection used
// by the public membership endpoint.
func SetValidatorSetProvider(provider validatorSetProvider) {
	publicValidatorSetProvider.mu.Lock()
	defer publicValidatorSetProvider.mu.Unlock()
	publicValidatorSetProvider.provider = provider
}

func currentValidatorSetProvider() validatorSetProvider {
	publicValidatorSetProvider.mu.RLock()
	defer publicValidatorSetProvider.mu.RUnlock()
	return publicValidatorSetProvider.provider
}

// PublicValidator contains only consensus membership and public production or
// slashing metadata. Local registration timestamps and account state are
// deliberately excluded.
type PublicValidator struct {
	Address        string                `json:"address"`
	Stake          float64               `json:"stake"`
	Status         chain.ValidatorStatus `json:"status"`
	SlashCount     int                   `json:"slash_count"`
	TotalSlashed   float64               `json:"total_slashed"`
	BlocksProduced uint64                `json:"blocks_produced"`
}

// ValidatorsResponse is the stable response envelope for GET /api/v1/validators.
type ValidatorsResponse struct {
	Count       int               `json:"count"`
	ActiveCount int               `json:"active_count"`
	Epoch       uint64            `json:"epoch"`
	TotalStake  float64           `json:"total_stake"`
	Validators  []PublicValidator `json:"validators"`
}

// ValidatorsHandler exposes the public, chain-derived validator membership.
func (h *Handlers) ValidatorsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeErrorResponse(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	provider := currentValidatorSetProvider()
	if provider == nil {
		writeErrorResponse(w, http.StatusServiceUnavailable,
			"validator membership is not configured on this node")
		return
	}

	addresses := provider.RegisteredAddresses()
	sort.Strings(addresses)
	response := ValidatorsResponse{
		ActiveCount: len(provider.ActiveValidators()),
		Epoch:       provider.CurrentEpoch(),
		Validators:  make([]PublicValidator, 0, len(addresses)),
	}
	for _, address := range addresses {
		validator, ok := provider.GetValidator(address)
		if !ok || validator == nil {
			continue
		}
		view := PublicValidator{
			Address:        validator.Address,
			Stake:          validator.Stake,
			Status:         validator.Status,
			SlashCount:     validator.SlashCount,
			TotalSlashed:   validator.TotalSlashed,
			BlocksProduced: validator.BlocksProduced,
		}
		response.Validators = append(response.Validators, view)
		response.TotalStake += view.Stake
	}
	response.Count = len(response.Validators)

	writeJSONResponse(w, http.StatusOK, response)
}
