package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/dagger/dagger/engine"
	internalengine "github.com/dagger/dagger/internal/engine"
	"github.com/dagger/dagger/router"
	"github.com/go-git/go-git/v5"
	"github.com/google/uuid"
	"github.com/spf13/cobra"
)

var (
	userAgentCfg userAgents
)

func sessionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "session",
		Long:         "WARNING: this is an internal-only command used by Dagger SDKs to communicate with the Dagger engine. It is not intended to be used by humans directly.",
		Hidden:       true,
		RunE:         EngineSession,
		SilenceUsage: true,
	}
	cmd.Flags().Var(&userAgentCfg, "ua", "user-agent keys to pass to registry (e.g, --ua 'sdk:python' --ua 'sdk_version:0.5.2' --ua 'sdk_async:false')")
	return cmd
}

type connectParams struct {
	Port         int    `json:"port"`
	SessionToken string `json:"session_token"`
}

func isCI() bool {
	return os.Getenv("CI") != "" || // GitHub Actions, Travis CI, CircleCI, Cirrus CI, GitLab CI, AppVeyor, CodeShip, dsari
		os.Getenv("BUILD_NUMBER") != "" || // Jenkins, TeamCity
		os.Getenv("RUN_ID") != "" // TaskCluster, dsari
}

func getGitInfo() (string, string, error) {
	repo, err := git.PlainOpen(".")
	if err != nil {
		return "", "", err
	}

	config, err := repo.Config()
	if err != nil {
		return "", "", err
	}

	committerEmail := config.User.Email
	committerHash := fmt.Sprintf("%x", sha256.Sum256([]byte(committerEmail)))

	remote, err := repo.Remote("origin")
	var repoURL string
	if err == nil {
		remoteConfig := remote.Config()
		if len(remoteConfig.URLs) > 0 {
			repoURL = remoteConfig.URLs[0]
		} else {
			return "", "", fmt.Errorf("remote origin URL not found")
		}
	} else {
		return "", "", err
	}

	repoHash := fmt.Sprintf("%x", sha256.Sum256([]byte(repoURL)))

	return strings.TrimSpace(committerHash), strings.TrimSpace(repoHash), nil
}

func setupUserAgent() {
	committerHash, repoHash, err := getGitInfo()
	if err == nil {
		userAgentCfg.Set("committer_hash:" + committerHash)
		userAgentCfg.Set("repo_hash:" + repoHash)
	}

	isCIValue := "false"
	if isCI() {
		isCIValue = "true"
	}
	userAgentCfg.Set("ci:" + isCIValue)
}

func EngineSession(cmd *cobra.Command, args []string) error {
	sessionToken, err := uuid.NewRandom()
	if err != nil {
		return err
	}

	setupUserAgent()

	startOpts := &engine.Config{
		Workdir:      workdir,
		ConfigPath:   configPath,
		LogOutput:    os.Stderr,
		RunnerHost:   internalengine.RunnerHost(),
		SessionToken: sessionToken.String(),
		JournalURI:   os.Getenv("_EXPERIMENTAL_DAGGER_JOURNAL"),
		UserAgent:    userAgentCfg.String(),
	}

	signalCh := make(chan os.Signal, 1)
	signal.Notify(signalCh, syscall.SIGINT, syscall.SIGTERM)

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}

	// shutdown if requested via signal
	go func() {
		<-signalCh
		l.Close()
	}()

	// shutdown if our parent closes stdin
	go func() {
		io.Copy(io.Discard, os.Stdin)
		l.Close()
	}()

	port := l.Addr().(*net.TCPAddr).Port

	return engine.Start(context.Background(), startOpts, func(ctx context.Context, r *router.Router) error {
		srv := http.Server{
			Handler:           r,
			ReadHeaderTimeout: 30 * time.Second,
		}

		paramBytes, err := json.Marshal(connectParams{
			Port:         port,
			SessionToken: sessionToken.String(),
		})
		if err != nil {
			return err
		}
		paramBytes = append(paramBytes, '\n')
		go func() {
			if _, err := os.Stdout.Write(paramBytes); err != nil {
				panic(err)
			}
		}()

		err = srv.Serve(l)
		// if error is "use of closed network connection", it's expected
		if err != nil && !errors.Is(err, net.ErrClosed) {
			return err
		}
		return nil
	})
}

type userAgents []userAgent

type userAgent struct {
	Key   string
	Value string
}

func (kv *userAgents) Type() string {
	return "useragents"
}

func (kv *userAgents) Set(s string) error {
	parts := strings.Split(s, ":")
	if len(parts) != 2 {
		return fmt.Errorf("bad format in --ua: '%s' (expected key:value)", s)
	}

	ua := userAgent{
		Key:   parts[0],
		Value: parts[1],
	}

	*kv = append(*kv, ua)

	return nil
}

func (kv *userAgents) String() string {
	var uas string
	for _, ua := range *kv {
		uas += fmt.Sprintf("%s:%s,", ua.Key, ua.Value)
	}
	return uas
}
