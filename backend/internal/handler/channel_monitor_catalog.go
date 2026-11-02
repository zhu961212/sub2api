package handler

import (
	"context"
	"errors"
	"sort"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// Catalog visibility always comes from the user's authorized group IDs. Monitor
// associations are a presentation hint, never an authorization decision.
type channelMonitorCatalogChannel struct {
	Name        string                       `json:"name"`
	Description string                       `json:"description"`
	Groups      []channelMonitorCatalogGroup `json:"groups"`
}

type channelMonitorCatalogGroup struct {
	ID                 int64                        `json:"id"`
	Name               string                       `json:"name"`
	Platform           string                       `json:"platform"`
	RateMultiplier     float64                      `json:"rate_multiplier"`
	UserRateMultiplier *float64                     `json:"user_rate_multiplier,omitempty"`
	AvailableModels    []channelMonitorCatalogModel `json:"available_models"`
	MonitorIDs         []int64                      `json:"monitor_ids"`
	restrictModels     bool
}

type channelMonitorCatalogModel struct {
	Name     string `json:"name"`
	Platform string `json:"platform"`
}

type channelMonitorCatalogReader interface {
	ListEnabledMonitors(context.Context) ([]*service.ChannelMonitor, error)
}

type channelMonitorCatalogAccountReader interface {
	GetByIDs(context.Context, []int64) ([]*service.Account, error)
	ListByGroup(context.Context, int64) ([]service.Account, error)
}

type channelMonitorCatalogAPIKeyReader interface {
	GetByKey(context.Context, string) (*service.APIKey, error)
}

// MonitorCatalog supplies channel/group rates and configured model names to both
// status implementations. Its availability follows the monitor feature, not the
// separate optional available-channels page.
// GET /api/v1/channel-monitors/catalog
func (h *AvailableChannelHandler) MonitorCatalog(c *gin.Context) {
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	if h.settingService == nil || !h.settingService.GetChannelMonitorRuntime(c.Request.Context()).Enabled {
		response.Success(c, []channelMonitorCatalogChannel{})
		return
	}
	groups, err := h.apiKeyService.GetAvailableGroups(c.Request.Context(), subject.UserID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	rates, err := h.apiKeyService.GetUserGroupRates(c.Request.Context(), subject.UserID)
	if err != nil {
		// Do not advertise an incorrect rate when the user's override is unknown.
		response.ErrorFrom(c, err)
		return
	}
	channels, err := h.channelService.ListAvailable(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	catalog := buildChannelMonitorCatalog(channels, groups, rates, c.GetHeader("Accept-Language"))
	if err := h.enrichMonitorCatalog(c.Request.Context(), catalog, channels, groups); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	c.Writer.Header().Add("Vary", "Accept-Language")
	response.Success(c, catalog)
}

func buildChannelMonitorCatalog(channels []service.AvailableChannel, allowedGroups []service.Group, rates map[int64]float64, language string) []channelMonitorCatalogChannel {
	allowed := make(map[int64]service.Group, len(allowedGroups))
	allowedIDs := make(map[int64]struct{}, len(allowedGroups))
	for _, group := range allowedGroups {
		allowed[group.ID] = group
		allowedIDs[group.ID] = struct{}{}
	}
	out := make([]channelMonitorCatalogChannel, 0, len(channels))
	attached := make(map[int64]struct{}, len(allowedGroups))
	configured := make(map[int64]struct{}, len(allowedGroups))
	for _, channel := range channels {
		for _, group := range channel.Groups {
			configured[group.ID] = struct{}{}
		}
		if channel.Status != service.StatusActive {
			continue
		}
		visible := filterUserVisibleGroups(channel.Groups, allowedIDs)
		if len(visible) == 0 {
			continue
		}
		localizeUserAvailableGroups(visible, language)
		row := channelMonitorCatalogChannel{Name: channel.Name, Description: channel.Description, Groups: make([]channelMonitorCatalogGroup, 0, len(visible))}
		supportedModels := channel.MonitorSupportedModels
		if supportedModels == nil {
			// Compatibility with readers that predate the internal monitor view.
			supportedModels = channel.SupportedModels
		}
		for _, group := range visible {
			models := make([]channelMonitorCatalogModel, 0)
			seen := make(map[channelMonitorCatalogModel]struct{})
			for _, model := range supportedModels {
				// SupportedModels already expands channel mappings/pricing to
				// concrete names; retain only models this particular group admits.
				if model.Name == "" || model.Platform == "" || strings.Contains(model.Name, "*") ||
					(group.Platform != service.PlatformComposite && model.Platform != group.Platform) ||
					!allowed[group.ID].ModelAllowlist.Allows(model.Name) {
					continue
				}
				item := channelMonitorCatalogModel{Name: model.Name, Platform: model.Platform}
				if _, exists := seen[item]; !exists {
					seen[item] = struct{}{}
					models = append(models, item)
				}
			}
			sort.Slice(models, func(i, j int) bool {
				if models[i].Platform != models[j].Platform {
					return models[i].Platform < models[j].Platform
				}
				return models[i].Name < models[j].Name
			})
			entry := channelMonitorCatalogGroup{ID: group.ID, Name: group.Name, Platform: group.Platform, RateMultiplier: group.RateMultiplier, AvailableModels: models, MonitorIDs: []int64{}, restrictModels: channel.RestrictModels}
			if rate, exists := rates[group.ID]; exists {
				entry.UserRateMultiplier = &rate
			}
			row.Groups = append(row.Groups, entry)
			attached[group.ID] = struct{}{}
		}
		out = append(out, row)
	}
	// Groups can route accounts without belonging to a pricing channel. Requiring
	// a channel association hid these groups (and all their rates) from V1 users.
	for _, group := range allowedGroups {
		if _, exists := attached[group.ID]; exists {
			continue
		}
		if _, exists := configured[group.ID]; exists {
			// An explicitly inactive channel must remain hidden.
			continue
		}
		name, description := group.I18n.Localize(language, group.Name, group.Description)
		entry := channelMonitorCatalogGroup{
			ID: group.ID, Name: name, Platform: group.Platform,
			RateMultiplier: group.RateMultiplier, AvailableModels: []channelMonitorCatalogModel{}, MonitorIDs: []int64{},
		}
		if rate, exists := rates[group.ID]; exists {
			entry.UserRateMultiplier = &rate
		}
		out = append(out, channelMonitorCatalogChannel{
			Name: entry.Name, Description: description,
			Groups: []channelMonitorCatalogGroup{entry},
		})
	}
	return out
}

func (h *AvailableChannelHandler) enrichMonitorCatalog(ctx context.Context, catalog []channelMonitorCatalogChannel, channels []service.AvailableChannel, groups []service.Group) error {
	if len(catalog) == 0 {
		return nil
	}
	groupByID := make(map[int64]service.Group, len(groups))
	for _, group := range groups {
		groupByID[group.ID] = group
	}
	entries := make(map[int64][]*channelMonitorCatalogGroup)
	accountsByGroup := make(map[int64][]service.Account)
	for i := range catalog {
		for j := range catalog[i].Groups {
			entry := &catalog[i].Groups[j]
			entries[entry.ID] = append(entries[entry.ID], entry)
			group := groupByID[entry.ID]
			if entry.restrictModels {
				continue
			}
			// Exact allowlist entries and group pricing are explicit configuration;
			// wildcard patterns are never advertised as concrete model names.
			for _, model := range group.ModelAllowlist.Models {
				if group.ModelAllowlist.Enabled && group.Platform != service.PlatformComposite {
					addMonitorCatalogModel(entry, group, model, group.Platform)
				}
			}
			for _, pricing := range group.ModelPricing {
				for _, model := range pricing.Models {
					addMonitorCatalogModel(entry, group, model, pricing.Platform)
				}
			}
			if h.catalogAccounts != nil {
				accounts, loaded := accountsByGroup[group.ID]
				if !loaded {
					var err error
					accounts, err = h.catalogAccounts.ListByGroup(ctx, group.ID)
					if err != nil {
						return err
					}
					accountsByGroup[group.ID] = accounts
				}
				for k := range accounts {
					account := &accounts[k]
					if !monitorCatalogPlatformMatches(group.Platform, account.Platform) {
						continue
					}
					for model := range account.GetModelMapping() {
						addMonitorCatalogModel(entry, group, model, account.Platform)
					}
				}
			}
		}
	}
	if h.catalogMonitors != nil && h.settingService.GetChannelMonitorRuntime(ctx).Mode == service.ChannelMonitorModeV1 {
		monitors, err := h.catalogMonitors.ListEnabledMonitors(ctx)
		if err != nil {
			return err
		}
		accountIDs := make([]int64, 0)
		for _, monitor := range monitors {
			if monitor != nil && monitor.AccountID != nil && *monitor.AccountID > 0 {
				accountIDs = append(accountIDs, *monitor.AccountID)
			}
		}
		linkedAccounts := make(map[int64]*service.Account)
		if len(accountIDs) > 0 && h.catalogAccounts != nil {
			accounts, err := h.catalogAccounts.GetByIDs(ctx, accountIDs)
			if err != nil {
				return err
			}
			for _, account := range accounts {
				if account != nil {
					linkedAccounts[account.ID] = account
				}
			}
		}
		resolvedKeys := make(map[string]*service.APIKey)
		for _, monitor := range monitors {
			if monitor == nil {
				continue
			}
			ids, err := h.monitorCatalogGroupIDs(ctx, monitor, groups, channels, linkedAccounts, resolvedKeys)
			if err != nil {
				return err
			}
			for _, id := range ids {
				for _, entry := range entries[id] {
					entry.MonitorIDs = append(entry.MonitorIDs, monitor.ID)
					if !entry.restrictModels && monitor.CheckMode != service.MonitorCheckModeQuota {
						group := groupByID[id]
						addMonitorCatalogModel(entry, group, monitor.PrimaryModel, monitor.Provider)
						for _, model := range monitor.ExtraModels {
							addMonitorCatalogModel(entry, group, model, monitor.Provider)
						}
					}
				}
			}
		}
	}
	for _, groupEntries := range entries {
		for _, entry := range groupEntries {
			sort.Slice(entry.MonitorIDs, func(i, j int) bool { return entry.MonitorIDs[i] < entry.MonitorIDs[j] })
			sort.Slice(entry.AvailableModels, func(i, j int) bool {
				if entry.AvailableModels[i].Platform != entry.AvailableModels[j].Platform {
					return entry.AvailableModels[i].Platform < entry.AvailableModels[j].Platform
				}
				return entry.AvailableModels[i].Name < entry.AvailableModels[j].Name
			})
		}
	}
	return nil
}

func (h *AvailableChannelHandler) monitorCatalogGroupIDs(ctx context.Context, monitor *service.ChannelMonitor, groups []service.Group, channels []service.AvailableChannel, accounts map[int64]*service.Account, resolvedKeys map[string]*service.APIKey) ([]int64, error) {
	if monitor.AccountID != nil && *monitor.AccountID > 0 {
		return monitorCatalogGroupIDs(monitor, groups, channels, accounts), nil
	}
	if monitor.APIKeyDecryptFailed {
		return nil, nil
	}
	key := strings.TrimSpace(monitor.APIKey)
	if key != "" && h.catalogAPIKeys != nil {
		apiKey, loaded := resolvedKeys[key]
		if !loaded {
			var err error
			apiKey, err = h.catalogAPIKeys.GetByKey(ctx, key)
			if err != nil && !errors.Is(err, service.ErrAPIKeyNotFound) {
				return nil, err
			}
			resolvedKeys[key] = apiKey
		}
		if apiKey != nil {
			// A local probe key identifies its actual routing group regardless of
			// the monitor's editable display labels. Keep visibility constrained
			// to the requesting user's groups, including when the key is owned
			// by the administrator who configured the monitor.
			if apiKey.GroupID != nil {
				for _, group := range groups {
					if group.ID == *apiKey.GroupID && monitorCatalogPlatformMatches(group.Platform, monitor.Provider) {
						return []int64{group.ID}, nil
					}
				}
			}
			return nil, nil
		}
	}
	// External probe keys cannot be resolved locally. Retain the legacy exact
	// configuration-name association for those monitors.
	return monitorCatalogGroupIDs(monitor, groups, channels, accounts), nil
}

func monitorCatalogPlatformMatches(groupPlatform, provider string) bool {
	return provider != "" && (groupPlatform == provider || groupPlatform == service.PlatformComposite)
}

func addMonitorCatalogModel(entry *channelMonitorCatalogGroup, group service.Group, name, platform string) {
	name = strings.TrimSpace(name)
	if name == "" || strings.ContainsAny(name, "*?") || !monitorCatalogPlatformMatches(group.Platform, platform) || !group.ModelAllowlist.Allows(name) {
		return
	}
	for _, existing := range entry.AvailableModels {
		if existing.Platform == platform && strings.EqualFold(existing.Name, name) {
			return
		}
	}
	entry.AvailableModels = append(entry.AvailableModels, channelMonitorCatalogModel{Name: name, Platform: platform})
}

func monitorCatalogGroupIDs(monitor *service.ChannelMonitor, groups []service.Group, channels []service.AvailableChannel, accounts map[int64]*service.Account) []int64 {
	if monitor.AccountID != nil && *monitor.AccountID > 0 {
		// An explicit source is authoritative, including an unavailable/deleted
		// account. Never reinterpret its free-text display labels as another group.
		account := accounts[*monitor.AccountID]
		if account == nil || account.Platform != monitor.Provider {
			return nil
		}
		ids := make([]int64, 0)
		for _, group := range groups {
			if !monitorCatalogPlatformMatches(group.Platform, monitor.Provider) {
				continue
			}
			for _, id := range account.GroupIDs {
				if id == group.ID {
					ids = append(ids, id)
					break
				}
			}
		}
		return ids
	}
	// Legacy probes have no group ID. Only an unambiguous exact configuration
	// name on the same provider can supply a display association.
	for _, label := range []string{strings.TrimSpace(monitor.GroupName), strings.TrimSpace(monitor.Name)} {
		if label == "" {
			continue
		}
		matches := make([]int64, 0)
		for _, group := range groups {
			if group.Name == label && monitorCatalogPlatformMatches(group.Platform, monitor.Provider) {
				matches = append(matches, group.ID)
			}
		}
		if len(matches) == 1 {
			return matches
		}
		if len(matches) > 1 {
			return nil
		}
		for _, channel := range channels {
			if channel.Status != service.StatusActive || channel.Name != label {
				continue
			}
			for _, ref := range channel.Groups {
				for _, group := range groups {
					if ref.ID == group.ID && monitorCatalogPlatformMatches(group.Platform, monitor.Provider) {
						matches = append(matches, group.ID)
					}
				}
			}
		}
		if len(matches) == 1 {
			return matches
		}
		if len(matches) > 1 {
			return nil
		}
	}
	return nil
}
