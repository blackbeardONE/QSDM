package main

import "fmt"

type blockProductionRole string

const (
	blockProductionRoleSolo            blockProductionRole = "solo"
	blockProductionRoleNetworkProducer blockProductionRole = "network-producer"
	blockProductionRoleNetworkFollower blockProductionRole = "network-follower"
)

func resolveBlockProductionRole(solo, networkProducer bool) (blockProductionRole, error) {
	if solo && networkProducer {
		return "", fmt.Errorf("QSDM_SOLO_VALIDATOR_MODE and QSDM_NETWORK_BLOCK_PRODUCER cannot both be enabled")
	}
	if solo {
		return blockProductionRoleSolo, nil
	}
	if networkProducer {
		return blockProductionRoleNetworkProducer, nil
	}
	return blockProductionRoleNetworkFollower, nil
}

func (r blockProductionRole) localProductionEnabled() bool {
	return r == blockProductionRoleSolo || r == blockProductionRoleNetworkProducer
}
