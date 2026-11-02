package service

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestQuotaResetLocationCachePreservesZoneSemantics(t *testing.T) {
	const zone = "America/New_York"
	const workers = 16
	locations := make([]*time.Location, workers)
	var wait sync.WaitGroup
	for i := range locations {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			location, err := loadQuotaResetLocation(zone)
			if err == nil {
				locations[index] = location
			}
		}(i)
	}
	wait.Wait()
	require.NotNil(t, locations[0])
	for _, location := range locations {
		require.Same(t, locations[0], location)
	}
	standard, err := time.LoadLocation(zone)
	require.NoError(t, err)
	for _, instant := range []string{"2026-03-08T06:30:00Z", "2026-03-08T07:30:00Z", "2026-11-01T05:30:00Z", "2026-11-01T06:30:00Z"} {
		now, err := time.Parse(time.RFC3339, instant)
		require.NoError(t, err)
		require.Equal(t, now.In(standard).Format(time.RFC3339), now.In(locations[0]).Format(time.RFC3339))
	}
	for _, name := range []string{"", "UTC"} {
		location, err := loadQuotaResetLocation(name)
		require.NoError(t, err)
		require.Same(t, time.UTC, location)
	}
	local, err := loadQuotaResetLocation("Local")
	require.NoError(t, err)
	require.Same(t, time.Local, local)
	_, cached := quotaResetLocations.Load("Local")
	require.False(t, cached, "Local must follow the application zone, not a cached old pointer")
	_, err = loadQuotaResetLocation("invalid/not-a-quota-zone")
	require.Error(t, err)
	_, cached = quotaResetLocations.Load("invalid/not-a-quota-zone")
	require.False(t, cached)
}

func BenchmarkQuotaResetLocation(b *testing.B) {
	b.Run("reload_named_zone", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_, _ = time.LoadLocation("Asia/Shanghai")
		}
	})
	b.Run("cached_named_zone", func(b *testing.B) {
		_, _ = loadQuotaResetLocation("Asia/Shanghai")
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, _ = loadQuotaResetLocation("Asia/Shanghai")
		}
	})
}
