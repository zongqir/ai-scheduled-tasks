package config

import (
	"fmt"
	"sort"
	"strings"
)

type TagRoutes map[string][]TagRouteTarget

type TagRouteTarget struct {
	Channel    string `json:"channel"`
	ChannelRef string `json:"channel_ref,omitempty"`
}

func normalizeTagRouteKey(raw string) string {
	return strings.ToLower(strings.TrimSpace(raw))
}

func normalizeTagRouteTargets(targets []TagRouteTarget) []TagRouteTarget {
	if len(targets) == 0 {
		return nil
	}

	normalized := make([]TagRouteTarget, 0, len(targets))
	seen := make(map[string]struct{}, len(targets))
	for _, target := range targets {
		channelName := strings.TrimSpace(target.Channel)
		channelRef := strings.TrimSpace(target.ChannelRef)
		if channelName == "" {
			continue
		}
		key := channelName + "\x00" + channelRef
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		normalized = append(normalized, TagRouteTarget{
			Channel:    channelName,
			ChannelRef: channelRef,
		})
	}
	return normalized
}

func (c Config) ResolveTagRouteTargets(tags []string) []TagRouteTarget {
	if len(tags) == 0 || len(c.TagRoutes) == 0 {
		return nil
	}

	var resolved []TagRouteTarget
	seen := make(map[string]struct{})
	for _, tag := range tags {
		for _, target := range c.TagRoutes[normalizeTagRouteKey(tag)] {
			channelName := strings.TrimSpace(target.Channel)
			channelRef := strings.TrimSpace(target.ChannelRef)
			if channelName == "" {
				continue
			}
			key := channelName + "\x00" + channelRef
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			resolved = append(resolved, TagRouteTarget{
				Channel:    channelName,
				ChannelRef: channelRef,
			})
		}
	}
	return resolved
}

func (c *Config) SetTagRoute(tag string, targets []TagRouteTarget) {
	tag = normalizeTagRouteKey(tag)
	if tag == "" {
		return
	}
	if c.TagRoutes == nil {
		c.TagRoutes = make(TagRoutes)
	}
	c.TagRoutes[tag] = normalizeTagRouteTargets(targets)
}

func (c *Config) RemoveTagRoute(tag string) bool {
	tag = normalizeTagRouteKey(tag)
	if tag == "" || len(c.TagRoutes) == 0 {
		return false
	}
	if _, ok := c.TagRoutes[tag]; !ok {
		return false
	}
	delete(c.TagRoutes, tag)
	if len(c.TagRoutes) == 0 {
		c.TagRoutes = nil
	}
	return true
}

func (c Config) SortedTagRouteKeys() []string {
	if len(c.TagRoutes) == 0 {
		return nil
	}
	keys := make([]string, 0, len(c.TagRoutes))
	for key := range c.TagRoutes {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func (c Config) validateTagRoutes() error {
	if len(c.TagRoutes) == 0 {
		return nil
	}

	normalized := make(TagRoutes, len(c.TagRoutes))
	for rawTag, targets := range c.TagRoutes {
		tag := normalizeTagRouteKey(rawTag)
		if tag == "" {
			return fmt.Errorf("tag_routes contains an empty tag")
		}
		normalizedTargets := normalizeTagRouteTargets(targets)
		if len(normalizedTargets) == 0 {
			return fmt.Errorf("tag_routes.%s must contain at least one channel", tag)
		}
		for _, target := range normalizedTargets {
			if err := c.ValidateChannelTarget(target.Channel, target.ChannelRef); err != nil {
				return fmt.Errorf("tag_routes.%s has invalid channel target: %w", tag, err)
			}
		}
		normalized[tag] = normalizedTargets
	}
	c.TagRoutes = normalized
	return nil
}

func (c *Config) applyTagRouteDefaults() {
	if len(c.TagRoutes) == 0 {
		c.TagRoutes = nil
		return
	}

	keys := c.SortedTagRouteKeys()
	normalized := make(TagRoutes, len(keys))
	for _, key := range keys {
		normalized[normalizeTagRouteKey(key)] = normalizeTagRouteTargets(c.TagRoutes[key])
	}
	c.TagRoutes = normalized
}
