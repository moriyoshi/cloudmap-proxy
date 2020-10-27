package main

import (
	"context"
	"sync"
	"time"
)

var now = time.Now

type serviceInstanceIdPair struct {
	serviceId  string
	instanceId string
}

type sdCacheEntry struct {
	e  time.Time
	sd *ServiceDescriptor
}

type sdCache map[serviceInstanceIdPair]sdCacheEntry

type UplookerCache struct {
	mu              sync.RWMutex
	ttl             time.Duration
	backingUplooker ServiceUplooker
	entries         sdCache
}

func NewUplookerCache(backing ServiceUplooker, cacheTtl time.Duration) *UplookerCache {
	return &UplookerCache{
		ttl:             cacheTtl,
		backingUplooker: backing,
		entries:         make(sdCache),
	}
}

func (c *UplookerCache) LookupService(ctx context.Context, serviceId, instanceId string) (*ServiceDescriptor, error) {
	c.mu.RLock()
	t := now()
	k := serviceInstanceIdPair{serviceId, instanceId}
	if v, ok := c.entries[k]; ok {
		if t.Before(v.e) {
			c.mu.RUnlock()
			return v.sd, nil
		}
	}
	c.mu.RUnlock()
	c.mu.Lock()
	defer c.mu.Unlock()
	if v, ok := c.entries[k]; ok {
		if t.Before(v.e) {
			return v.sd, nil
		}
	}
	sd, err := c.backingUplooker.LookupService(ctx, serviceId, instanceId)
	if err != nil {
		return nil, err
	}
	c.entries[k] = sdCacheEntry{t.Add(c.ttl), sd}
	return sd, nil
}
