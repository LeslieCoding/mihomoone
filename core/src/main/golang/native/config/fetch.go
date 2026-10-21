package config

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	U "net/url"
	"os"
	P "path"
	"runtime"
	"strconv"
	"strings"
	"time"

	"cfa/native/app"

	"github.com/metacubex/mihomo/adapter/provider"
	"github.com/metacubex/mihomo/common/yaml"
	clashHttp "github.com/metacubex/mihomo/component/http"
	"github.com/metacubex/mihomo/log"
	RB "github.com/metacubex/mihomo/rules/bundle"
)

type Status struct {
	Action            string   `json:"action"`
	Args              []string `json:"args"`
	Progress          int      `json:"progress"`
	MaxProgress       int      `json:"max"`
	SubUpload         *int64   `json:"subUpload,omitempty"`
	SubDownload       *int64   `json:"subDownload,omitempty"`
	SubTotal          *int64   `json:"subTotal,omitempty"`
	SubExpire         *int64   `json:"subExpire,omitempty"`
	SubUpdateInterval *int64   `json:"subUpdateInterval,omitempty"`
}

type fetchHeader struct {
	SubscriptionUserInfo  string
	ProfileUpdateInterval string
}

var bootstrapProxies = []string{
	"http://127.0.0.1:7890",   // local core mixed port (when the core is running)
	"http://192.168.5.6:7897", // LAN bootstrap proxy (PC portproxy)
}

func headerOf(h interface{ Get(string) string }) fetchHeader {
	return fetchHeader{
		SubscriptionUserInfo:  h.Get("subscription-userinfo"),
		ProfileUpdateInterval: h.Get("profile-update-interval"),
	}
}

func openUrlDirect(ctx context.Context, url string, header http.Header) (io.ReadCloser, fetchHeader, error) {
	response, err := clashHttp.HttpRequest(ctx, url, http.MethodGet, header, nil)
	if err != nil {
		return nil, fetchHeader{}, err
	}

	if response.StatusCode >= 400 {
		_ = response.Body.Close()

		return nil, fetchHeader{}, fmt.Errorf("http status %d", response.StatusCode)
	}

	return response.Body, headerOf(response.Header), nil
}

func openUrlViaProxy(ctx context.Context, url string, header http.Header, proxy string) (io.ReadCloser, fetchHeader, error) {
	proxyUrl, err := U.Parse(proxy)
	if err != nil {
		return nil, fetchHeader{}, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fetchHeader{}, err
	}
	for k, v := range header {
		for _, s := range v {
			req.Header.Add(k, s)
		}
	}

	transport := &http.Transport{
		Proxy:               http.ProxyURL(proxyUrl),
		DisableKeepAlives:   true,
		TLSHandshakeTimeout: 10 * time.Second,
	}

	client := &http.Client{Transport: transport, Timeout: 30 * time.Second}

	response, err := client.Do(req)
	if err != nil {
		return nil, fetchHeader{}, err
	}

	if response.StatusCode >= 400 {
		_ = response.Body.Close()

		return nil, fetchHeader{}, fmt.Errorf("http status %d via %s", response.StatusCode, proxy)
	}

	return response.Body, headerOf(response.Header), nil
}

func openUrl(ctx context.Context, url string) (io.ReadCloser, fetchHeader, error) {
	header := http.Header{"User-Agent": {"ClashMetaForAndroid/" + app.VersionName()}}

	reader, fh, err := openUrlDirect(ctx, url, header)

	// The subscription host may be unreachable on a direct connection (e.g.
	// blocked by the firewall). Fall back to known bootstrap proxies.
	if err != nil {
		for _, proxy := range bootstrapProxies {
			if reader, fh, err = openUrlViaProxy(ctx, url, header, proxy); err == nil {
				break
			}
		}
	}

	return reader, fh, err
}

// validateConfigFile rejects broken subscription payloads (empty body, HTML
// error pages, v2board zero-proxy templates) so a bad download never
// overwrites the last known good config.
func validateConfigFile(file string) error {
	data, err := os.ReadFile(file)
	if err != nil {
		return err
	}

	var probe struct {
		Proxy         []map[string]any `yaml:"proxies"`
		ProxyProvider map[string]any   `yaml:"proxy-providers"`
	}
	if err := yaml.Unmarshal(data, &probe); err != nil {
		return fmt.Errorf("parse downloaded config: %s", err.Error())
	}
	if len(probe.Proxy) == 0 && len(probe.ProxyProvider) == 0 {
		return errors.New("downloaded config contains no proxies")
	}

	return nil
}

func openContent(url string) (io.ReadCloser, error) {
	return app.OpenContent(url)
}

func fetchOnce(ctx context.Context, url *U.URL, file string, validate func(string) error) (fetchHeader, error) {
	var reader io.ReadCloser
	var header fetchHeader
	var err error

	switch url.Scheme {
	case "http", "https":
		reader, header, err = openUrl(ctx, url.String())
	case "content":
		reader, err = openContent(url.String())
	default:
		err = fmt.Errorf("unsupported scheme %s of %s", url.Scheme, url)
	}

	if err != nil {
		return fetchHeader{}, err
	}

	defer reader.Close()

	// Write to a temp file first, validate, then atomically move into place:
	// a failed or invalid download must not destroy the existing file.
	tmp := file + ".partial"
	if err := writeFile(tmp, reader); err != nil {
		_ = os.Remove(tmp)
		return fetchHeader{}, err
	}

	if validate != nil {
		if err := validate(tmp); err != nil {
			_ = os.Remove(tmp)
			return fetchHeader{}, err
		}
	}

	if err := os.Rename(tmp, file); err != nil {
		_ = os.Remove(tmp)
		return fetchHeader{}, err
	}

	return header, nil
}

func fetch(url *U.URL, file string, validate func(string) error) (fetchHeader, error) {
	var header fetchHeader
	var lastErr error

	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			time.Sleep(time.Duration(3*attempt) * time.Second)
		}

		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		header, lastErr = fetchOnce(ctx, url, file, validate)
		cancel()

		if lastErr == nil {
			return header, nil
		}
	}

	return fetchHeader{}, lastErr
}

func writeFile(file string, reader io.Reader) error {
	_ = os.MkdirAll(P.Dir(file), 0700)

	f, err := os.OpenFile(file, os.O_WRONLY|os.O_TRUNC|os.O_CREATE, 0600)
	if err != nil {
		return err
	}

	defer f.Close()

	_, err = io.Copy(f, reader)
	if err != nil {
		_ = os.Remove(file)
	}

	return err
}

func parseProfileUpdateInterval(value string) (int64, bool) {
	hours, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err != nil {
		return 0, false
	}

	if hours <= 0 {
		return 0, true
	}

	interval := time.Duration(hours) * time.Hour
	if interval < 15*time.Minute {
		interval = 15 * time.Minute
	}

	return int64(interval / time.Millisecond), true
}

func reportSubscriptionInfo(header fetchHeader, reportStatus func(string)) {
	userinfo := header.SubscriptionUserInfo
	updateIntervalHeader := header.ProfileUpdateInterval
	if userinfo == "" && updateIntervalHeader == "" {
		return
	}

	status := Status{
		Action:      "SubscriptionInfo",
		Args:        []string{},
		Progress:    -1,
		MaxProgress: -1,
	}

	if userinfo != "" {
		info := provider.NewSubscriptionInfo(userinfo)
		expire := info.Expire * 1000
		status.SubUpload = &info.Upload
		status.SubDownload = &info.Download
		status.SubTotal = &info.Total
		status.SubExpire = &expire
	}

	if interval, ok := parseProfileUpdateInterval(updateIntervalHeader); ok {
		status.SubUpdateInterval = &interval
	}

	bytes, _ := json.Marshal(&status)
	reportStatus(string(bytes))
}

func FetchAndValid(
	path string,
	url string,
	force bool,
	reportStatus func(string),
) error {
	configPath := P.Join(path, "config.yaml")

	if _, err := os.Stat(configPath); os.IsNotExist(err) || force {
		url, err := U.Parse(url)
		if err != nil {
			return err
		}

		bytes, _ := json.Marshal(&Status{
			Action:      "FetchConfiguration",
			Args:        []string{url.Host},
			Progress:    -1,
			MaxProgress: -1,
		})

		reportStatus(string(bytes))

		header, err := fetch(url, configPath, validateConfigFile)
		if err != nil {
			return err
		}

		reportSubscriptionInfo(header, reportStatus)
	}

	defer runtime.GC()

	rawCfg, err := UnmarshalAndPatch(path)
	if err != nil {
		return err
	}

	forEachProviders(rawCfg, func(index int, total int, name string, provider map[string]any, prefix string) {
		bytes, _ := json.Marshal(&Status{
			Action:      "FetchProviders",
			Args:        []string{name},
			Progress:    index,
			MaxProgress: total,
		})

		reportStatus(string(bytes))

		u, uok := provider["url"]
		p, pok := provider["path"]

		if !uok || !pok {
			return
		}

		us, uok := u.(string)
		ps, pok := p.(string)

		if !uok || !pok {
			return
		}

		if _, err := os.Stat(ps); err == nil {
			return
		}

		url, err := U.Parse(us)
		if err != nil {
			return
		}

		if prefix == RULES {
			if pib, uok := provider["path-in-bundle"]; uok {
				if pib, uok := pib.(string); uok && pib != "" {
					// actually, we don't need to extract the file here; the core will do it.
					// however, due to historical reasons, CMFA fetches provider content when loading profile,
					// so we maintain consistency with the old behavior.
					if file, err := RB.Open(pib); err == nil {
						defer file.Close()
						if err := writeFile(ps, file); err == nil {
							return
						}
					}
				}
			}
		}

		_, err = fetch(url, ps, nil)
		if err != nil {
			log.Warnln("Fetch provider %s: %s", us, err.Error())
		}
	})

	bytes, _ := json.Marshal(&Status{
		Action:      "Verifying",
		Args:        []string{},
		Progress:    0xffff,
		MaxProgress: 0xffff,
	})

	reportStatus(string(bytes))

	cfg, err := Parse(rawCfg)
	if err != nil {
		return err
	}

	destroyProviders(cfg)

	return nil
}
