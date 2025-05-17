package submesh

import (
    "sync"
    "errors"
    "fmt"
)

// DynamicSubmesh represents a dynamic submesh with priority routing rules.
type DynamicSubmesh struct {
    Name          string
    FeeThreshold  float64
    PriorityLevel int
    GeoTags       []string
}

// DynamicSubmeshManager manages dynamic submeshes and routing rules.
type DynamicSubmeshManager struct {
    Mu        sync.RWMutex
    Submeshes map[string]*DynamicSubmesh
}

// NewDynamicSubmeshManager creates a new DynamicSubmeshManager.
func NewDynamicSubmeshManager() *DynamicSubmeshManager {
    return &DynamicSubmeshManager{
        Submeshes: make(map[string]*DynamicSubmesh),
    }
}

// ApplyGovernanceUpdate applies governance voting results to update submesh rules.
func (m *DynamicSubmeshManager) ApplyGovernanceUpdate(updates []*DynamicSubmesh) {
    m.Mu.Lock()
    defer m.Mu.Unlock()
    for _, update := range updates {
        m.Submeshes[update.Name] = update
    }
}

// AddOrUpdateSubmesh adds or updates a dynamic submesh.
func (m *DynamicSubmeshManager) AddOrUpdateSubmesh(ds *DynamicSubmesh) {
    m.Mu.Lock()
    defer m.Mu.Unlock()
    m.Submeshes[ds.Name] = ds
}

// RemoveSubmesh removes a dynamic submesh by name.
func (m *DynamicSubmeshManager) RemoveSubmesh(name string) error {
    m.Mu.Lock()
    defer m.Mu.Unlock()
    if _, exists := m.Submeshes[name]; !exists {
        return errors.New("submesh not found")
    }
    delete(m.Submeshes, name)
    return nil
}

// GetSubmesh returns a dynamic submesh by name.
func (m *DynamicSubmeshManager) GetSubmesh(name string) (*DynamicSubmesh, error) {
    m.Mu.RLock()
    defer m.Mu.RUnlock()
    ds, exists := m.Submeshes[name]
    if !exists {
        return nil, errors.New("submesh not found")
    }
    return ds, nil
}

// RouteTransaction determines the routing priority for a transaction based on fee and geo tag.
func (m *DynamicSubmeshManager) RouteTransaction(fee float64, geoTag string) (*DynamicSubmesh, error) {
    m.Mu.RLock()
    defer m.Mu.RUnlock()

    var selected *DynamicSubmesh
    for _, ds := range m.Submeshes {
        if fee >= ds.FeeThreshold {
            for _, tag := range ds.GeoTags {
                if tag == geoTag {
                    if selected == nil || ds.PriorityLevel > selected.PriorityLevel {
                        selected = ds
                    }
                }
            }
        }
    }
    if selected == nil {
        return nil, fmt.Errorf("no matching submesh for fee %.4f and geoTag %s", fee, geoTag)
    }
    return selected, nil
}
