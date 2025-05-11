package consensus

import (
    "errors"

    "github.com/blackbeardONE/QSDM/internal/logging"
    "github.com/blackbeardONE/QSDM/pkg/crypto"
)

// ProofOfEntanglement represents the PoE consensus mechanism
type ProofOfEntanglement struct {
    dilithium *crypto.Dilithium
}

// NewProofOfEntanglement creates a new PoE instance with Dilithium crypto
func NewProofOfEntanglement() *ProofOfEntanglement {
    return &ProofOfEntanglement{
        dilithium: crypto.NewDilithium(),
    }
}

// Sign signs a message using Dilithium
func (poe *ProofOfEntanglement) Sign(message []byte) ([]byte, error) {
    return poe.dilithium.Sign(message)
}

// ValidateTransaction validates a transaction by checking 2 parent cells and signatures
func (poe *ProofOfEntanglement) ValidateTransaction(tx []byte, parentCells [][]byte, signatures [][]byte) (bool, error) {
    if len(parentCells) != 2 {
        logging.Error.Println("Invalid number of parent cells, expected 2")
        return false, errors.New("invalid number of parent cells, expected 2")
    }
    if len(signatures) == 0 {
        logging.Error.Println("No signatures provided")
        return false, errors.New("no signatures provided")
    }
    // Verify signatures using Dilithium
    for _, sig := range signatures {
        valid, err := poe.dilithium.Verify(tx, sig)
        if err != nil || !valid {
            logging.Error.Println("Signature verification failed")
            return false, errors.New("signature verification failed")
        }
    }
    logging.Info.Println("Transaction validated with Proof-of-Entanglement consensus")
    return true, nil
}
