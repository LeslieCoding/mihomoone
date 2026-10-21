package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/dlclark/regexp2"

	"cfa/native/common"

	"github.com/metacubex/mihomo/common/utils"
	"github.com/metacubex/mihomo/config"
	C "github.com/metacubex/mihomo/constant"
	"github.com/metacubex/mihomo/log"
)

var processors = []processor{
	patchExternalController, // must before patchOverride, so we only apply ExternalController in Override settings
	patchOverride,
	patchGeneral,
	patchProfile,
	patchDns,
	patchTun,
	patchListeners,
	patchProviders,
	patchMihomoOneAutoSelect, // after patchProviders, before validConfig
	validConfig,
}

type processor func(cfg *config.RawConfig, profileDir string) error

func patchOverride(cfg *config.RawConfig, _ string) error {
	if err := json.NewDecoder(strings.NewReader(ReadOverride(OverrideSlotPersist))).Decode(cfg); err != nil {
		log.Warnln("Apply persist override: %s", err.Error())
	}
	if err := json.NewDecoder(strings.NewReader(ReadOverride(OverrideSlotSession))).Decode(cfg); err != nil {
		log.Warnln("Apply session override: %s", err.Error())
	}

	return nil
}

func patchExternalController(cfg *config.RawConfig, _ string) error {
	cfg.ExternalController = ""
	cfg.ExternalControllerTLS = ""

	return nil
}

func patchGeneral(cfg *config.RawConfig, profileDir string) error {
	cfg.Interface = ""
	cfg.RoutingMark = 0
	if cfg.ExternalController != "" || cfg.ExternalControllerTLS != "" {
		cfg.ExternalUI = profileDir + "/ui"
	}

	// Performance/stability: unified-delay measures the full handshake+request
	// latency (more truthful node ranking); tcp-concurrent dials all candidates
	// of a group at once, cutting connect latency after a node switch.
	cfg.UnifiedDelay = true
	cfg.TCPConcurrent = true

	return nil
}

func patchProfile(cfg *config.RawConfig, _ string) error {
	cfg.Profile.StoreSelected = false
	cfg.Profile.StoreFakeIP = true

	return nil
}

func patchDns(cfg *config.RawConfig, _ string) error {
	if !cfg.DNS.Enable {
		cfg.DNS = config.DefaultRawConfig().DNS
		cfg.DNS.Enable = true
		cfg.DNS.NameServer = defaultNameServers
		cfg.DNS.EnhancedMode = C.DNSFakeIP
		cfg.DNS.FakeIPRange = defaultFakeIPRange
		cfg.DNS.FakeIPFilter = defaultFakeIPFilter

		cfg.ClashForAndroid.AppendSystemDNS = true
	}

	if cfg.ClashForAndroid.AppendSystemDNS {
		cfg.DNS.NameServer = append(cfg.DNS.NameServer, "system://")
	}

	return nil
}

func patchTun(cfg *config.RawConfig, _ string) error {
	cfg.Tun.Enable = false
	cfg.Tun.AutoRoute = false
	cfg.Tun.AutoDetectInterface = false
	return nil
}

func patchListeners(cfg *config.RawConfig, _ string) error {
	newListeners := make([]map[string]any, 0, len(cfg.Listeners))
	for _, mapping := range cfg.Listeners {
		if proxyType, existType := mapping["type"].(string); existType {
			switch proxyType {
			case "tproxy", "redir", "tun":
				continue // remove those listeners which is not supported
			}
		}
		newListeners = append(newListeners, mapping)
	}
	cfg.Listeners = newListeners
	return nil
}

func patchProviders(cfg *config.RawConfig, profileDir string) error {
	forEachProviders(cfg, func(index int, total int, key string, provider map[string]any, prefix string) {
		path, _ := provider["path"].(string)
		if len(path) > 0 {
			path = common.ResolveAsRoot(path)
		} else if url, ok := provider["url"].(string); ok {
			path = prefix + "/" + utils.MakeHash([]byte(url)).String() // same as C.GetPathByHash
		} else {
			return // both path and url are empty, maybe inline provider
		}
		provider["path"] = profileDir + "/providers/" + path
	})

	return nil
}

// Info entries published by the subscription (traffic counters etc.) are not
// real nodes; never let them into the auto group.
var mihomoOneInfoPattern = regexp2.MustCompile(`(?i)(剩余流量|套餐到期|下次重置|距离|官网|expire|traffic)`, 0)

// Auto-select rule aligned with desktop ProxyOne v3.2.4 (2026-10-21):
// region is NO LONGER restricted to SG/JP — any region qualifies, but only
// Hysteria/Hysteria2 protocol nodes (AnyTLS etc. measured slow, excluded).
// The url-test group "MihomoOne" pins the lowest-latency one and is placed
// first in every select group (StoreSelected is disabled, so the first entry
// is the effective default on every load). lazy=false keeps delays probed
// proactively so a dead node is detected within one interval, not on use.
func patchMihomoOneAutoSelect(cfg *config.RawConfig, _ string) error {
	isHysteria := func(t string) bool {
		switch strings.ToLower(t) {
		case "hysteria", "hysteria2":
			return true
		}
		return false
	}

	var hysteriaProxies []any
	for _, p := range cfg.Proxy {
		name, _ := p["name"].(string)
		if name == "" {
			continue
		}
		proxyType, _ := p["type"].(string)
		if isHysteria(proxyType) {
			hysteriaProxies = append(hysteriaProxies, name)
		}
	}

	autoGroup := map[string]any{
		"name":      "MihomoOne",
		"type":      "url-test",
		"url":       "http://www.gstatic.com/generate_204",
		"interval":  60,
		"tolerance": 100,
		"lazy":      false,
	}

	if len(hysteriaProxies) > 0 {
		autoGroup["proxies"] = hysteriaProxies
	} else if len(cfg.Proxy) > 0 {
		// Fail-safe: subscription has inline proxies but none speak Hysteria.
		// Include all non-info nodes rather than emitting a broken empty group.
		for _, p := range cfg.Proxy {
			name, _ := p["name"].(string)
			if name == "" {
				continue
			}
			if matched, _ := mihomoOneInfoPattern.MatchString(name); matched {
				continue
			}
			hysteriaProxies = append(hysteriaProxies, name)
		}
		autoGroup["proxies"] = hysteriaProxies
	} else {
		// No inline proxies: nodes may live in proxy-providers, which resolve
		// at runtime. Reference all providers; protocol can only be filtered
		// by name here (subscription convention: -A<digit> suffix = AnyTLS,
		// plain numeric suffix = Hysteria2), info entries excluded as well.
		var providerNames []any
		for name := range cfg.ProxyProvider {
			providerNames = append(providerNames, name)
		}

		if len(providerNames) == 0 {
			return nil
		}

		autoGroup["use"] = providerNames
		autoGroup["exclude-filter"] = `(?i)(-A\d+$|剩余流量|套餐到期|重置|expire|traffic)`
	}

	if _, useProviders := autoGroup["use"]; !useProviders {
		if proxies, _ := autoGroup["proxies"].([]any); len(proxies) == 0 {
			return nil
		}
	}

	hasAutoGroup := false
	for _, g := range cfg.ProxyGroup {
		name, _ := g["name"].(string)
		if name == "MihomoOne" {
			hasAutoGroup = true
			break
		}
	}
	if !hasAutoGroup {
		cfg.ProxyGroup = append([]map[string]any{autoGroup}, cfg.ProxyGroup...)
	}

	for _, g := range cfg.ProxyGroup {
		groupType, _ := g["type"].(string)
		name, _ := g["name"].(string)
		if groupType != "select" || name == "MihomoOne" {
			continue
		}

		proxies, _ := g["proxies"].([]any)
		contains := false
		for _, p := range proxies {
			if s, _ := p.(string); s == "MihomoOne" {
				contains = true
				break
			}
		}
		if !contains {
			g["proxies"] = append([]any{"MihomoOne"}, proxies...)
		}
	}

	return nil
}

func validConfig(cfg *config.RawConfig, _ string) error {
	if len(cfg.Proxy) == 0 && len(cfg.ProxyProvider) == 0 {
		return errors.New("profile does not contain `proxies` or `proxy-providers`")
	}

	if _, err := regexp2.Compile(cfg.ClashForAndroid.UiSubtitlePattern, 0); err != nil {
		return fmt.Errorf("compile ui-subtitle-pattern: %s", err.Error())
	}

	return nil
}

func process(cfg *config.RawConfig, profileDir string) error {
	for _, p := range processors {
		if err := p(cfg, profileDir); err != nil {
			return err
		}
	}

	return nil
}
