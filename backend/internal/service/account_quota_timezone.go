package service

import (
	"sync"
	"time"
)

// A scheduler scans many accounts with the same reset zone. Cache immutable
// successful IANA locations so each quota check does not reload zone data.
// Invalid input is never cached; Local follows the application's current zone.
var quotaResetLocations sync.Map

func loadQuotaResetLocation(name string) (*time.Location, error) {
	if name == "" || name == "UTC" {
		return time.UTC, nil
	}
	if name == "Local" {
		return time.Local, nil
	}
	if cached, ok := quotaResetLocations.Load(name); ok {
		if location, valid := cached.(*time.Location); valid {
			return location, nil
		}
	}
	location, err := time.LoadLocation(name)
	if err != nil {
		return nil, err
	}
	actual, _ := quotaResetLocations.LoadOrStore(name, location)
	if cached, ok := actual.(*time.Location); ok {
		return cached, nil
	}
	quotaResetLocations.Store(name, location)
	return location, nil
}
