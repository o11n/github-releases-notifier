package main

import (
	"context"
	"os"
	"strings"
	"time"

	"github.com/alexflint/go-arg"
	"github.com/go-kit/kit/log"
	"github.com/go-kit/kit/log/level"
	"github.com/joho/godotenv"
	githubql "github.com/shurcooL/githubql"
	"golang.org/x/oauth2"
)

// Config of env and args
type Config struct {
	GithubToken     string        `arg:"env:GITHUB_TOKEN"`
	GitlabAPIToken  string        `arg:"env:GITLAB_API_TOKEN"`
	GitlabHostname  string        `arg:"env:GITLAB_HOSTNAME"`
	GitlabProjectID int           `arg:"env:GITLAB_PROJECT_ID"`
	GitlabLabels    string        `arg:"env:GITLAB_LABELS"`
	Interval        time.Duration `arg:"env:INTERVAL"`
	LogLevel        string        `arg:"env:LOG_LEVEL"`
	Repositories    []string      `arg:"-r,separate"`
	SlackHook       string        `arg:"env:SLACK_HOOK"`
	IgnoreNonstable bool          `arg:"env:IGNORE_NONSTABLE"`
}

// Token returns an oauth2 token or an error.
func (c Config) Token() *oauth2.Token {
	return &oauth2.Token{AccessToken: c.GithubToken}
}

func main() {
	_ = godotenv.Load()

	c := Config{
		Interval: time.Hour,
		LogLevel: "info",
	}
	arg.MustParse(&c)

	logger := log.NewJSONLogger(log.NewSyncWriter(os.Stdout))
	logger = log.With(logger,
		"ts", log.DefaultTimestampUTC,
		"caller", log.Caller(5),
	)

	// level.SetKey("severity")
	switch strings.ToLower(c.LogLevel) {
	case "debug":
		logger = level.NewFilter(logger, level.AllowDebug())
	case "warn":
		logger = level.NewFilter(logger, level.AllowWarn())
	case "error":
		logger = level.NewFilter(logger, level.AllowError())
	default:
		logger = level.NewFilter(logger, level.AllowInfo())
	}

	if len(c.Repositories) == 0 {
		level.Error(logger).Log("msg", "no repositories wo watch")
		os.Exit(1)
	}

	tokenSource := oauth2.StaticTokenSource(c.Token())
	client := oauth2.NewClient(context.Background(), tokenSource)
	checker := &Checker{
		logger: logger,
		client: githubql.NewClient(client),
	}

	// TODO: releases := make(chan Repository, len(c.Repositories))
	releases := make(chan Repository)
	go checker.Run(c.Interval, c.Repositories, releases)

	slack := SlackSender{Hook: c.SlackHook}
	gitlab := GitlabSender{
		Hostname:  c.GitlabHostname,
		APIToken:  c.GitlabAPIToken,
		ProjectID: c.GitlabProjectID,
		Labels:    c.GitlabLabels,
		logger:    logger,
	}

	level.Info(logger).Log("msg", "waiting for new releases")
	for repository := range releases {
		if c.IgnoreNonstable && repository.Release.IsNonstable() {
			level.Debug(logger).Log("msg", "not notifying about non-stable version", "version", repository.Release.Name)
			continue
		}
		if c.SlackHook != "" {
			if err := slack.Send(repository); err != nil {
				level.Warn(logger).Log(
					"msg", "failed to send release to messenger",
					"err", err,
				)
				continue
			}
		}
		if c.GitlabAPIToken != "" && c.GitlabHostname != "" && c.GitlabProjectID > 0 {
			if err := gitlab.Send(repository); err != nil {
				level.Warn(logger).Log(
					"msg", "failed to send release to messenger",
					"err", err,
				)
				continue
			}
		}
	}
}
