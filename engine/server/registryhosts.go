package server

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/core/remotes/docker"
	localcontentstore "github.com/containerd/containerd/v2/plugins/content/local"
	"github.com/dagger/dagger/engine/distconsts"
	resolverconfig "github.com/dagger/dagger/internal/buildkit/util/resolver/config"
	"github.com/dagger/dagger/internal/buildkit/util/tracing"
	"github.com/pkg/errors"
)

const defaultRegistryPath = "/v2"

func newRegistryHosts(registries map[string]resolverconfig.RegistryConfig) docker.RegistryHosts {
	return docker.Registries(
		func(host string) ([]docker.RegistryHost, error) {
			originHost := host
			if originHost == "docker.io" {
				originHost = "registry-1.docker.io"
			}

			originCfg := registries[host]
			if host != originHost {
				if normalizedCfg, ok := registries[originHost]; ok {
					originCfg = normalizedCfg
				}
			}

			out := make([]docker.RegistryHost, 0, len(originCfg.Mirrors)+1)
			for _, rawMirror := range originCfg.Mirrors {
				mirrorHost, mirrorPath := extractMirrorHostAndPath(rawMirror)
				mirrorCfg, ok := registries[mirrorHost]
				if !ok {
					mirrorCfg = originCfg
				}
				h, err := applyRegistryHostConfig(mirrorHost, mirrorCfg, docker.RegistryHost{
					Scheme:       "https",
					Host:         mirrorHost,
					Path:         path.Join(defaultRegistryPath, mirrorPath),
					Capabilities: docker.HostCapabilityPull | docker.HostCapabilityResolve,
				})
				if err != nil {
					return nil, err
				}
				out = append(out, h)
			}

			h, err := applyRegistryHostConfig(originHost, originCfg, docker.RegistryHost{
				Scheme:       "https",
				Host:         originHost,
				Path:         defaultRegistryPath,
				Capabilities: docker.HostCapabilityPush | docker.HostCapabilityPull | docker.HostCapabilityResolve,
			})
			if err != nil {
				return nil, err
			}
			out = append(out, h)
			return out, nil
		},
		docker.ConfigureDefaultRegistries(
			docker.WithPlainHTTP(docker.MatchLocalhost),
		),
	)
}

func applyRegistryHostConfig(host string, cfg resolverconfig.RegistryConfig, h docker.RegistryHost) (docker.RegistryHost, error) {
	tc, err := loadRegistryTLSConfig(cfg)
	if err != nil {
		return docker.RegistryHost{}, err
	}

	isHTTP := false
	if cfg.PlainHTTP != nil && *cfg.PlainHTTP {
		isHTTP = true
	} else if cfg.PlainHTTP == nil {
		if ok, _ := docker.MatchLocalhost(host); ok {
			isHTTP = true
		}
	}

	if cfg.Insecure != nil && *cfg.Insecure {
		tc.InsecureSkipVerify = true
	}
	baseTransport := docker.DefaultHTTPTransport(tc)
	h.Client = &http.Client{
		Transport: tracing.NewTransport(baseTransport),
	}
	explicitTLS := tc.InsecureSkipVerify || tc.RootCAs != nil || len(tc.Certificates) > 0

	if !isHTTP {
		return h, nil
	}

	_, port, _ := net.SplitHostPort(host)
	if explicitTLS && port != "80" {
		h.Scheme = "https"
		h.Client = &http.Client{
			Transport: tracing.NewTransport(docker.NewHTTPFallback(baseTransport)),
		}
		return h, nil
	}

	h.Scheme = "http"
	return h, nil
}

func loadRegistryTLSConfig(cfg resolverconfig.RegistryConfig) (*tls.Config, error) {
	for _, dir := range cfg.TLSConfigDir {
		entries, err := os.ReadDir(dir)
		if err != nil && !errors.Is(err, os.ErrNotExist) && !errors.Is(err, os.ErrPermission) {
			return nil, errors.WithStack(err)
		}
		for _, entry := range entries {
			if strings.HasSuffix(entry.Name(), ".crt") {
				cfg.RootCAs = append(cfg.RootCAs, filepath.Join(dir, entry.Name()))
			}
			if strings.HasSuffix(entry.Name(), ".cert") {
				cfg.KeyPairs = append(cfg.KeyPairs, resolverconfig.TLSKeyPair{
					Certificate: filepath.Join(dir, entry.Name()),
					Key:         filepath.Join(dir, strings.TrimSuffix(entry.Name(), ".cert")+".key"),
				})
			}
		}
	}

	tc := &tls.Config{}
	if len(cfg.RootCAs) > 0 {
		systemPool, err := x509.SystemCertPool()
		if err != nil {
			if runtime.GOOS == "windows" {
				systemPool = x509.NewCertPool()
			} else {
				return nil, errors.Wrap(err, "unable to get system cert pool")
			}
		}
		tc.RootCAs = systemPool
	}

	for _, caPath := range cfg.RootCAs {
		dt, err := os.ReadFile(caPath)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to read %s", caPath)
		}
		tc.RootCAs.AppendCertsFromPEM(dt)
	}

	for _, kp := range cfg.KeyPairs {
		cert, err := tls.LoadX509KeyPair(kp.Certificate, kp.Key)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to load keypair for %s", kp.Certificate)
		}
		tc.Certificates = append(tc.Certificates, cert)
	}
	return tc, nil
}

func extractMirrorHostAndPath(mirror string) (string, string) {
	var mirrorPath string
	mirrorHost := mirror

	u, err := url.Parse(mirror)
	if err != nil || u.Host == "" {
		u, err = url.Parse(fmt.Sprintf("//%s", mirror))
	}
	if err != nil || u.Host == "" {
		return mirrorHost, mirrorPath
	}

	return u.Host, strings.TrimRight(u.Path, "/")
}

func openBuiltinOCIStore() (content.Store, error) {
	return localcontentstore.NewStore(distconsts.EngineContainerBuiltinContentDir)
}
