package submesh

import (
	"testing"
)

func BenchmarkRouteTransaction(b *testing.B) {
	dsManager := NewDynamicSubmeshManager()
	
	// Add multiple submeshes
	dsManager.AddOrUpdateSubmesh(&DynamicSubmesh{
		Name:          "fastlane",
		FeeThreshold:  0.01,
		PriorityLevel: 10,
		GeoTags:       []string{"US", "EU"},
	})
	dsManager.AddOrUpdateSubmesh(&DynamicSubmesh{
		Name:          "slowlane",
		FeeThreshold:  0.001,
		PriorityLevel: 5,
		GeoTags:       []string{"ASIA"},
	})

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := dsManager.RouteTransaction(0.02, "US")
		if err != nil {
			b.Fatalf("Failed to route transaction: %v", err)
		}
	}
}

func BenchmarkAddOrUpdateSubmesh(b *testing.B) {
	dsManager := NewDynamicSubmeshManager()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		submesh := &DynamicSubmesh{
			Name:          "submesh_" + string(rune(i)),
			FeeThreshold:  0.01,
			PriorityLevel: 10,
			GeoTags:       []string{"US"},
		}
		dsManager.AddOrUpdateSubmesh(submesh)
	}
}

