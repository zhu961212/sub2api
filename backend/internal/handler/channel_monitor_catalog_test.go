package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type monitorCatalogRuntimeStub struct {
	enabled bool
	mode    string
}

func (s monitorCatalogRuntimeStub) GetChannelMonitorRuntime(context.Context) service.ChannelMonitorRuntime {
	return service.ChannelMonitorRuntime{Enabled: s.enabled, Mode: s.mode}
}
func (s monitorCatalogRuntimeStub) GetAvailableChannelsRuntime(context.Context) service.AvailableChannelsRuntime {
	// The separate available-channels page remains disabled in these tests.
	return service.AvailableChannelsRuntime{Enabled: false}
}

type monitorCatalogChannelStub struct {
	channels []service.AvailableChannel
	err      error
}

func (s monitorCatalogChannelStub) ListAvailable(context.Context) ([]service.AvailableChannel, error) {
	return s.channels, s.err
}

type monitorCatalogGroupsStub struct {
	groups            []service.Group
	rates             map[int64]float64
	groupErr, rateErr error
}

func (s monitorCatalogGroupsStub) GetAvailableGroups(context.Context, int64) ([]service.Group, error) {
	return s.groups, s.groupErr
}
func (s monitorCatalogGroupsStub) GetUserGroupRates(context.Context, int64) (map[int64]float64, error) {
	return s.rates, s.rateErr
}

func monitorCatalogContext(authenticated bool) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/channel-monitors/catalog", nil)
	if authenticated {
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 42})
	}
	return c, w
}

func TestChannelMonitorCatalogRequiresAuthentication(t *testing.T) {
	c, w := monitorCatalogContext(false)
	(&AvailableChannelHandler{}).MonitorCatalog(c)
	require.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestChannelMonitorCatalogFollowsMonitorSwitch(t *testing.T) {
	for _, settings := range []availableChannelRuntimeReader{nil, monitorCatalogRuntimeStub{enabled: false, mode: "v1"}, monitorCatalogRuntimeStub{enabled: false, mode: "v2"}} {
		c, w := monitorCatalogContext(true)
		(&AvailableChannelHandler{settingService: settings}).MonitorCatalog(c)
		require.Equal(t, http.StatusOK, w.Code)
		require.JSONEq(t, `{"code":0,"message":"success","data":[]}`, w.Body.String())
	}
	c, w := monitorCatalogContext(true)
	NewAvailableChannelHandler(nil, nil, nil, nil, nil).MonitorCatalog(c)
	require.Equal(t, http.StatusOK, w.Code)
	require.JSONEq(t, `{"code":0,"message":"success","data":[]}`, w.Body.String())
}

func TestChannelMonitorCatalogWorksForBothModesWithoutAvailableChannels(t *testing.T) {
	for _, mode := range []string{"v1", "v2"} {
		t.Run(mode, func(t *testing.T) {
			c, w := monitorCatalogContext(true)
			h := AvailableChannelHandler{
				settingService: monitorCatalogRuntimeStub{enabled: true, mode: mode},
				apiKeyService:  monitorCatalogGroupsStub{groups: []service.Group{{ID: 1, Platform: "openai"}}, rates: map[int64]float64{1: 0}},
				channelService: monitorCatalogChannelStub{channels: []service.AvailableChannel{{
					Name: "Channel A", Status: service.StatusActive, Groups: []service.AvailableGroupRef{{ID: 1, Name: "Group A", Platform: "openai", RateMultiplier: 2}},
					SupportedModels: []service.SupportedModel{{Name: "gpt-6.1", Platform: "openai"}},
				}}},
			}
			h.MonitorCatalog(c)
			require.Equal(t, http.StatusOK, w.Code)
			var envelope struct {
				Data []channelMonitorCatalogChannel `json:"data"`
			}
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &envelope))
			require.Len(t, envelope.Data, 1)
			require.Len(t, envelope.Data[0].Groups, 1)
			group := envelope.Data[0].Groups[0]
			require.Equal(t, 2.0, group.RateMultiplier)
			require.NotNil(t, group.UserRateMultiplier)
			require.Zero(t, *group.UserRateMultiplier)
			require.Equal(t, []channelMonitorCatalogModel{{Name: "gpt-6.1", Platform: "openai"}}, group.AvailableModels)
			require.Contains(t, w.Header().Values("Vary"), "Accept-Language")
		})
	}
}

func TestChannelMonitorCatalogDoesNotSilentlyInventRatesOrVisibility(t *testing.T) {
	failure := errors.New("repository unavailable")
	for _, deps := range []struct {
		groups   monitorCatalogGroupsStub
		channels monitorCatalogChannelStub
	}{
		{groups: monitorCatalogGroupsStub{groupErr: failure}},
		{groups: monitorCatalogGroupsStub{rateErr: failure}},
		{channels: monitorCatalogChannelStub{err: failure}},
	} {
		c, w := monitorCatalogContext(true)
		h := AvailableChannelHandler{settingService: monitorCatalogRuntimeStub{enabled: true}, apiKeyService: deps.groups, channelService: deps.channels}
		h.MonitorCatalog(c)
		require.Equal(t, http.StatusInternalServerError, w.Code)
	}
}

func TestChannelMonitorCatalogUsesExactGroupAssociationsAndPerGroupModels(t *testing.T) {
	models := []service.SupportedModel{
		{Name: "gpt-6.1", Platform: "openai"}, {Name: "gpt-6.1", Platform: "openai"},
		{Name: "gpt-5.5", Platform: "openai"}, {Name: "claude-sonnet-4-6", Platform: "anthropic"},
		{Name: "gpt-*", Platform: "openai"}, {Name: "invalid-without-platform"},
	}
	refs := []service.AvailableGroupRef{
		{ID: 1, Name: "same-name", Platform: "openai", RateMultiplier: 2, I18n: service.GroupI18n{"zh": {Name: "中文分组"}}},
		{ID: 2, Name: "same-name", Platform: "openai", RateMultiplier: 1},
		{ID: 3, Name: "composite", Platform: service.PlatformComposite},
		{ID: 4, Name: "private", Platform: "openai"},
		{ID: 5, Name: "empty", Platform: "gemini"},
	}
	allowed := []service.Group{
		{ID: 1, ModelAllowlist: service.GroupModelAllowlist{Enabled: true, Models: []string{"gpt-6.1"}}},
		{ID: 2}, {ID: 3}, {ID: 5},
	}
	channels := []service.AvailableChannel{
		{Name: "Active", Status: service.StatusActive, Groups: refs, SupportedModels: models},
		{Name: "Disabled", Status: "inactive", Groups: refs, SupportedModels: models},
		{Name: "Hidden", Status: service.StatusActive, Groups: []service.AvailableGroupRef{{ID: 4}}, SupportedModels: models},
	}
	got := buildChannelMonitorCatalog(channels, allowed, map[int64]float64{1: 0.5, 4: 0.1}, "zh")
	require.Len(t, got, 1)
	require.Len(t, got[0].Groups, 4)
	require.Equal(t, "中文分组", got[0].Groups[0].Name)
	require.Equal(t, []channelMonitorCatalogModel{{Name: "gpt-6.1", Platform: "openai"}}, got[0].Groups[0].AvailableModels)
	require.Equal(t, 0.5, *got[0].Groups[0].UserRateMultiplier)
	require.Equal(t, []channelMonitorCatalogModel{{Name: "gpt-5.5", Platform: "openai"}, {Name: "gpt-6.1", Platform: "openai"}}, got[0].Groups[1].AvailableModels)
	require.Nil(t, got[0].Groups[1].UserRateMultiplier)
	require.Len(t, got[0].Groups[2].AvailableModels, 3)
	require.Empty(t, got[0].Groups[3].AvailableModels)
	raw, err := json.Marshal(got)
	require.NoError(t, err)
	for _, forbidden := range []string{"api_key", "endpoint", "pricing", "billing_model_source", "restrict_models", "private"} {
		require.NotContains(t, string(raw), forbidden)
	}
	require.Contains(t, string(raw), `"available_models":[]`)
}

type monitorCatalogMonitorStub struct {
	monitors []*service.ChannelMonitor
	err      error
}

func (s monitorCatalogMonitorStub) ListEnabledMonitors(context.Context) ([]*service.ChannelMonitor, error) {
	return s.monitors, s.err
}

type monitorCatalogAccountStub struct {
	linked  []*service.Account
	byGroup map[int64][]service.Account
	err     error
}

type monitorCatalogAPIKeyStub struct {
	keys  map[string]*service.APIKey
	err   error
	calls int
}

func (s *monitorCatalogAPIKeyStub) GetByKey(_ context.Context, key string) (*service.APIKey, error) {
	s.calls++
	if s.err != nil {
		return nil, s.err
	}
	if apiKey := s.keys[key]; apiKey != nil {
		return apiKey, nil
	}
	return nil, service.ErrAPIKeyNotFound
}

func (s monitorCatalogAccountStub) GetByIDs(context.Context, []int64) ([]*service.Account, error) {
	return s.linked, s.err
}

func (s monitorCatalogAccountStub) ListByGroup(_ context.Context, id int64) ([]service.Account, error) {
	return s.byGroup[id], s.err
}

func TestChannelMonitorCatalogIncludesGroupsWithoutPricingChannels(t *testing.T) {
	c, w := monitorCatalogContext(true)
	c.Request.Header.Set("Accept-Language", "zh")
	groups := []service.Group{{
		ID: 10, Name: "gpt-standard", Platform: service.PlatformOpenAI, RateMultiplier: 0.2,
		I18n:           service.GroupI18n{"zh": {Name: "稳定线路", Description: "公开分组"}},
		ModelAllowlist: service.GroupModelAllowlist{Enabled: true, Models: []string{"gpt-6-astra", "gpt-*"}},
		ModelPricing:   []service.ChannelModelPricing{{Platform: service.PlatformOpenAI, Models: []string{"gpt-6.1", "gpt-*"}}},
	}}
	h := AvailableChannelHandler{
		settingService: monitorCatalogRuntimeStub{enabled: true, mode: service.ChannelMonitorModeV1},
		apiKeyService:  monitorCatalogGroupsStub{groups: groups, rates: map[int64]float64{10: 0}},
		channelService: monitorCatalogChannelStub{},
		catalogMonitors: monitorCatalogMonitorStub{monitors: []*service.ChannelMonitor{{
			ID: 7, Name: "gpt-standard", Provider: service.MonitorProviderOpenAI, PrimaryModel: "gpt-6-astra",
			ExtraModels: []string{"gpt-6.1", "gpt-*"}, APIKey: "secret-key", Endpoint: "https://secret-upstream.example",
		}}},
		catalogAccounts: monitorCatalogAccountStub{byGroup: map[int64][]service.Account{10: {
			{Platform: service.PlatformOpenAI, Schedulable: false, Credentials: map[string]any{"model_mapping": map[string]any{"gpt-6-sol": "upstream-secret"}}},
			{Platform: service.PlatformAnthropic, Credentials: map[string]any{"model_mapping": map[string]any{"claude-private": "private"}}},
		}}},
	}
	h.MonitorCatalog(c)
	require.Equal(t, http.StatusOK, w.Code)
	var envelope struct {
		Data []channelMonitorCatalogChannel `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &envelope))
	require.Len(t, envelope.Data, 1)
	require.Equal(t, "稳定线路", envelope.Data[0].Name)
	require.Equal(t, "公开分组", envelope.Data[0].Description)
	group := envelope.Data[0].Groups[0]
	require.Equal(t, []int64{7}, group.MonitorIDs)
	require.Equal(t, 0.2, group.RateMultiplier)
	require.NotNil(t, group.UserRateMultiplier)
	require.Zero(t, *group.UserRateMultiplier)
	require.Equal(t, []channelMonitorCatalogModel{
		{Name: "gpt-6-astra", Platform: service.PlatformOpenAI},
		{Name: "gpt-6-sol", Platform: service.PlatformOpenAI},
		{Name: "gpt-6.1", Platform: service.PlatformOpenAI},
	}, group.AvailableModels)
	for _, secret := range []string{"secret-key", "secret-upstream", "upstream-secret", "claude-private", "gpt-*"} {
		require.NotContains(t, w.Body.String(), secret)
	}
}

func TestChannelMonitorCatalogKeepsInactiveChannelsHidden(t *testing.T) {
	groups := []service.Group{{ID: 1, Name: "disabled", Platform: service.PlatformOpenAI}, {ID: 2, Name: "unassigned", Platform: service.PlatformOpenAI}}
	channels := []service.AvailableChannel{{Status: "inactive", Groups: []service.AvailableGroupRef{{ID: 1}}}}
	got := buildChannelMonitorCatalog(channels, groups, nil, "en")
	require.Len(t, got, 1)
	require.Equal(t, int64(2), got[0].Groups[0].ID)
}

func TestChannelMonitorCatalogAssociatesOnlyUnambiguousProviderMatches(t *testing.T) {
	accountID := int64(50)
	groups := []service.Group{
		{ID: 1, Name: "same", Platform: service.PlatformOpenAI},
		{ID: 2, Name: "same", Platform: service.PlatformAnthropic},
		{ID: 3, Name: "duplicate", Platform: service.PlatformOpenAI},
		{ID: 4, Name: "duplicate", Platform: service.PlatformOpenAI},
		{ID: 5, Name: "linked", Platform: service.PlatformOpenAI},
		{ID: 6, Name: "other", Platform: service.PlatformOpenAI},
	}
	accounts := map[int64]*service.Account{accountID: {ID: accountID, Platform: service.PlatformOpenAI, GroupIDs: []int64{5, 999}}}
	for _, tt := range []struct {
		name    string
		monitor service.ChannelMonitor
		want    []int64
	}{
		{name: "exact name with same provider", monitor: service.ChannelMonitor{Name: "same", Provider: service.PlatformOpenAI}, want: []int64{1}},
		{name: "exact display group", monitor: service.ChannelMonitor{Name: "display", GroupName: "same", Provider: service.PlatformAnthropic}, want: []int64{2}},
		{name: "duplicate group is ambiguous", monitor: service.ChannelMonitor{Name: "duplicate", Provider: service.PlatformOpenAI}},
		{name: "provider alone cannot associate", monitor: service.ChannelMonitor{Name: "unrelated", Provider: service.PlatformOpenAI}},
		{name: "explicit account overrides labels", monitor: service.ChannelMonitor{Name: "same", GroupName: "same", Provider: service.PlatformOpenAI, AccountID: &accountID}, want: []int64{5}},
		{name: "explicit account provider mismatch", monitor: service.ChannelMonitor{Name: "same", Provider: service.PlatformAnthropic, AccountID: &accountID}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, monitorCatalogGroupIDs(&tt.monitor, groups, nil, accounts))
		})
	}
	delete(accounts, accountID)
	require.Nil(t, monitorCatalogGroupIDs(&service.ChannelMonitor{Name: "same", Provider: service.PlatformOpenAI, AccountID: &accountID}, groups, nil, accounts))
}

func TestChannelMonitorCatalogRestrictedModelsAndV2DoNotUseProbeModels(t *testing.T) {
	groups := []service.Group{{ID: 1, Name: "same", Platform: service.PlatformOpenAI}}
	channels := []service.AvailableChannel{{
		Name: "Channel", Status: service.StatusActive, RestrictModels: true,
		Groups:          []service.AvailableGroupRef{{ID: 1, Name: "same", Platform: service.PlatformOpenAI}},
		SupportedModels: []service.SupportedModel{{Name: "gpt-allowed", Platform: service.PlatformOpenAI}},
	}}
	for _, mode := range []string{service.ChannelMonitorModeV1, service.ChannelMonitorModeV2} {
		t.Run(mode, func(t *testing.T) {
			catalog := buildChannelMonitorCatalog(channels, groups, nil, "en")
			h := AvailableChannelHandler{
				settingService:  monitorCatalogRuntimeStub{enabled: true, mode: mode},
				catalogMonitors: monitorCatalogMonitorStub{monitors: []*service.ChannelMonitor{{ID: 4, Name: "same", Provider: service.PlatformOpenAI, PrimaryModel: "gpt-blocked"}}},
				catalogAccounts: monitorCatalogAccountStub{err: errors.New("restricted catalog must not enumerate accounts")},
			}
			require.NoError(t, h.enrichMonitorCatalog(context.Background(), catalog, channels, groups))
			require.Equal(t, []channelMonitorCatalogModel{{Name: "gpt-allowed", Platform: service.PlatformOpenAI}}, catalog[0].Groups[0].AvailableModels)
			if mode == service.ChannelMonitorModeV1 {
				require.Equal(t, []int64{4}, catalog[0].Groups[0].MonitorIDs)
			} else {
				require.Empty(t, catalog[0].Groups[0].MonitorIDs)
			}
		})
	}
}

func TestChannelMonitorCatalogEnrichesEveryChannelWithoutRelaxingItsModelRestriction(t *testing.T) {
	groups := []service.Group{{ID: 1, Name: "shared", Platform: service.PlatformOpenAI}}
	refs := []service.AvailableGroupRef{{ID: 1, Name: "shared", Platform: service.PlatformOpenAI}}
	channels := []service.AvailableChannel{
		{Name: "Restricted", Status: service.StatusActive, RestrictModels: true, Groups: refs,
			SupportedModels: []service.SupportedModel{{Name: "gpt-restricted", Platform: service.PlatformOpenAI}}},
		{Name: "Open", Status: service.StatusActive, Groups: refs},
	}
	catalog := buildChannelMonitorCatalog(channels, groups, nil, "en")
	h := AvailableChannelHandler{
		settingService: monitorCatalogRuntimeStub{enabled: true, mode: service.ChannelMonitorModeV1},
		catalogMonitors: monitorCatalogMonitorStub{monitors: []*service.ChannelMonitor{
			{ID: 9, Name: "shared", Provider: service.PlatformOpenAI, PrimaryModel: "gpt-probe"},
			{ID: 3, Name: "shared", Provider: service.PlatformOpenAI, PrimaryModel: "gpt-probe"},
		}},
		catalogAccounts: monitorCatalogAccountStub{byGroup: map[int64][]service.Account{1: {
			{Platform: service.PlatformOpenAI, Credentials: map[string]any{"model_mapping": map[string]any{"gpt-account": "private"}}},
		}}},
	}
	require.NoError(t, h.enrichMonitorCatalog(context.Background(), catalog, channels, groups))
	require.Equal(t, []int64{3, 9}, catalog[0].Groups[0].MonitorIDs)
	require.Equal(t, []int64{3, 9}, catalog[1].Groups[0].MonitorIDs)
	require.Equal(t, []channelMonitorCatalogModel{{Name: "gpt-restricted", Platform: service.PlatformOpenAI}}, catalog[0].Groups[0].AvailableModels)
	require.Equal(t, []channelMonitorCatalogModel{
		{Name: "gpt-account", Platform: service.PlatformOpenAI},
		{Name: "gpt-probe", Platform: service.PlatformOpenAI},
	}, catalog[1].Groups[0].AvailableModels)
}

func TestChannelMonitorCatalogUsesAuthoritativeRestrictedInventory(t *testing.T) {
	groups := []service.Group{{ID: 1, Name: "Group", Platform: service.PlatformOpenAI}}
	channels := []service.AvailableChannel{{
		Name: "Channel", Status: service.StatusActive, RestrictModels: true,
		Groups: []service.AvailableGroupRef{{ID: 1, Name: "Group", Platform: service.PlatformOpenAI}},
		SupportedModels: []service.SupportedModel{
			{Name: "allowed", Platform: service.PlatformOpenAI}, {Name: "mapping-without-channel-price", Platform: service.PlatformOpenAI},
		},
		MonitorSupportedModels: []service.SupportedModel{{Name: "allowed", Platform: service.PlatformOpenAI}},
	}}
	catalog := buildChannelMonitorCatalog(channels, groups, nil, "en")
	require.Equal(t, []channelMonitorCatalogModel{{Name: "allowed", Platform: service.PlatformOpenAI}}, catalog[0].Groups[0].AvailableModels)
	channels[0].MonitorSupportedModels = []service.SupportedModel{}
	catalog = buildChannelMonitorCatalog(channels, groups, nil, "en")
	require.Empty(t, catalog[0].Groups[0].AvailableModels, "all restricted models stay omitted even if the legacy view contains mappings")
}

func TestChannelMonitorCatalogAssociatesProbeKeyDespiteDifferentDisplayNames(t *testing.T) {
	c, w := monitorCatalogContext(true)
	groupID := int64(10)
	groups := []service.Group{{
		ID: groupID, Name: "gpt 低价", Platform: service.PlatformOpenAI, RateMultiplier: 0.1,
		ModelPricing: []service.ChannelModelPricing{{Platform: service.PlatformOpenAI, Models: []string{"gpt-6-astra", "gpt-6-sol"}}},
	}}
	keys := &monitorCatalogAPIKeyStub{keys: map[string]*service.APIKey{
		"probe-secret-key": {ID: 77, UserID: 1, GroupID: &groupID},
	}}
	h := AvailableChannelHandler{
		settingService: monitorCatalogRuntimeStub{enabled: true, mode: service.ChannelMonitorModeV1},
		apiKeyService:  monitorCatalogGroupsStub{groups: groups, rates: map[int64]float64{groupID: 0.05}},
		channelService: monitorCatalogChannelStub{},
		catalogAPIKeys: keys,
		catalogMonitors: monitorCatalogMonitorStub{monitors: []*service.ChannelMonitor{
			{ID: 3, Name: "gpt低价", GroupName: "custom label", Provider: service.PlatformOpenAI, APIKey: "probe-secret-key", PrimaryModel: "gpt-6-sol"},
			{ID: 8, Name: "another display name", Provider: service.PlatformOpenAI, APIKey: "probe-secret-key", PrimaryModel: "gpt-6-astra"},
		}},
	}
	h.MonitorCatalog(c)
	require.Equal(t, http.StatusOK, w.Code)
	var envelope struct {
		Data []channelMonitorCatalogChannel `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &envelope))
	require.Len(t, envelope.Data, 1)
	group := envelope.Data[0].Groups[0]
	require.Equal(t, []int64{3, 8}, group.MonitorIDs)
	require.Equal(t, "gpt 低价", group.Name)
	require.Equal(t, 0.1, group.RateMultiplier)
	require.Equal(t, 0.05, *group.UserRateMultiplier)
	require.Equal(t, []channelMonitorCatalogModel{
		{Name: "gpt-6-astra", Platform: service.PlatformOpenAI},
		{Name: "gpt-6-sol", Platform: service.PlatformOpenAI},
	}, group.AvailableModels)
	require.Equal(t, 1, keys.calls, "shared probe keys are resolved once per catalog request")
	require.NotContains(t, w.Body.String(), "probe-secret-key")
}

func TestChannelMonitorCatalogProbeKeyAssociationHonorsVisibilityAndExplicitSources(t *testing.T) {
	visibleID, hiddenID, incompatibleID, compositeID := int64(10), int64(99), int64(20), int64(30)
	accountID := int64(50)
	groups := []service.Group{
		{ID: visibleID, Name: "same", Platform: service.PlatformOpenAI},
		{ID: incompatibleID, Name: "other", Platform: service.PlatformAnthropic},
		{ID: compositeID, Name: "composite", Platform: service.PlatformComposite},
	}
	accounts := map[int64]*service.Account{accountID: {ID: accountID, Platform: service.PlatformOpenAI, GroupIDs: []int64{visibleID}}}
	for _, tt := range []struct {
		name    string
		monitor service.ChannelMonitor
		key     *service.APIKey
		want    []int64
	}{
		{name: "key overrides a conflicting label", monitor: service.ChannelMonitor{Name: "same", Provider: service.PlatformOpenAI, APIKey: "probe-key"}, key: &service.APIKey{GroupID: &compositeID}, want: []int64{compositeID}},
		{name: "hidden key group does not fall back to visible label", monitor: service.ChannelMonitor{Name: "same", Provider: service.PlatformOpenAI, APIKey: "probe-key"}, key: &service.APIKey{GroupID: &hiddenID}},
		{name: "wrong platform does not fall back to label", monitor: service.ChannelMonitor{Name: "same", Provider: service.PlatformOpenAI, APIKey: "probe-key"}, key: &service.APIKey{GroupID: &incompatibleID}},
		{name: "unassigned local key does not fall back to label", monitor: service.ChannelMonitor{Name: "same", Provider: service.PlatformOpenAI, APIKey: "probe-key"}, key: &service.APIKey{}},
		{name: "external key retains exact legacy association", monitor: service.ChannelMonitor{Name: "same", Provider: service.PlatformOpenAI, APIKey: "probe-key"}, want: []int64{visibleID}},
		{name: "account source takes precedence", monitor: service.ChannelMonitor{Name: "same", Provider: service.PlatformOpenAI, APIKey: "probe-key", AccountID: &accountID}, key: &service.APIKey{GroupID: &compositeID}, want: []int64{visibleID}},
		{name: "failed decryption does not fall back to label", monitor: service.ChannelMonitor{Name: "same", Provider: service.PlatformOpenAI, APIKeyDecryptFailed: true}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			h := AvailableChannelHandler{catalogAPIKeys: &monitorCatalogAPIKeyStub{keys: map[string]*service.APIKey{"probe-key": tt.key}}}
			got, err := h.monitorCatalogGroupIDs(context.Background(), &tt.monitor, groups, nil, accounts, make(map[string]*service.APIKey))
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestChannelMonitorCatalogDoesNotSilentlyFallBackOnProbeKeyLookupFailure(t *testing.T) {
	c, w := monitorCatalogContext(true)
	h := AvailableChannelHandler{
		settingService: monitorCatalogRuntimeStub{enabled: true, mode: service.ChannelMonitorModeV1},
		apiKeyService:  monitorCatalogGroupsStub{groups: []service.Group{{ID: 1, Name: "same", Platform: service.PlatformOpenAI}}},
		channelService: monitorCatalogChannelStub{},
		catalogAPIKeys: &monitorCatalogAPIKeyStub{err: errors.New("lookup unavailable for secret-key")},
		catalogMonitors: monitorCatalogMonitorStub{monitors: []*service.ChannelMonitor{
			{ID: 3, Name: "same", Provider: service.PlatformOpenAI, APIKey: "secret-key"},
		}},
	}
	h.MonitorCatalog(c)
	require.Equal(t, http.StatusInternalServerError, w.Code)
	require.NotContains(t, w.Body.String(), "secret-key")
}
