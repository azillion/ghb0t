package main

import (
	"context"
	"flag"
	"fmt"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/azillion/ghb0t/version"
	"github.com/blang/semver"
	"github.com/genuinetools/pkg/cli"
	"github.com/google/go-github/github"
	"github.com/sirupsen/logrus"
	"golang.org/x/oauth2"
	"gopkg.in/yaml.v2"
)

// Travis struct to unmarshal .travis.yml files
type Travis struct {
	GoVersions []string `yaml:"go,flow"`
}

var (
	token    string
	interval time.Duration
	enturl   string

	lastChecked time.Time

	debug          bool
	validGoVersion semver.Version
)

func init() {
	v, err := semver.ParseTolerant("1.10")
	if err != nil {
		logrus.Fatal(err)
	}
	validGoVersion = v
}

func main() {
	// Create a new cli program.
	p := cli.NewProgram()
	p.Name = "golint-fixer"
	p.Description = "A GitHub Bot to automatically create pull requests to fix golint imports."

	// Set the GitCommit and Version.
	p.GitCommit = version.GITCOMMIT
	p.Version = version.VERSION

	// Setup the global flags.
	p.FlagSet = flag.NewFlagSet("global", flag.ExitOnError)
	p.FlagSet.StringVar(&token, "token", os.Getenv("GITHUB_TOKEN"), "GitHub API token (or env var GITHUB_TOKEN)")
	p.FlagSet.DurationVar(&interval, "interval", 30*time.Second, "check interval (ex. 5ms, 10s, 1m, 3h)")
	p.FlagSet.StringVar(&enturl, "url", "", "Connect to a specific GitHub server, provide full API URL (ex. https://github.example.com/api/v3/)")

	p.FlagSet.BoolVar(&debug, "d", false, "enable debug logging")

	// Set the before function.
	p.Before = func(ctx context.Context) error {
		// Set the log level.
		if debug {
			logrus.SetLevel(logrus.DebugLevel)
		}

		if token == "" {
			return fmt.Errorf("GitHub token cannot be empty")
		}

		return nil
	}

	// Set the main program action.
	p.Action = func(ctx context.Context, args []string) error {
		ticker := time.NewTicker(interval)

		// On ^C, or SIGTERM handle exit.
		c := make(chan os.Signal, 1)
		signal.Notify(c, os.Interrupt)
		signal.Notify(c, syscall.SIGTERM)
		var cancel context.CancelFunc
		ctx, cancel = context.WithCancel(ctx)
		go func() {
			for sig := range c {
				cancel()
				ticker.Stop()
				logrus.Infof("Received %s, exiting.", sig.String())
				os.Exit(0)
			}
		}()

		// Create the http client.
		ts := oauth2.StaticTokenSource(
			&oauth2.Token{AccessToken: token},
		)
		tc := oauth2.NewClient(ctx, ts)

		// Create the github client.
		client := github.NewClient(tc)
		if enturl != "" {
			var err error
			client.BaseURL, err = url.Parse(enturl)
			if err != nil {
				logrus.Fatalf("failed to parse provided url: %v", err)
			}
		}

		// Get the authenticated user, the empty string being passed let's the GitHub
		// API know we want ourself.
		user, _, err := client.Users.Get(ctx, "")
		if err != nil {
			logrus.Fatal(err)
		}
		username := *user.Login

		logrus.Infof("Bot started for user %s.", username)

		reposChan := make(chan github.Repository, 100)
		forksChan := make(chan github.Repository, 100)

		var wg sync.WaitGroup
		wg.Add(3)
		go getSearchResults(ctx, client, 1, reposChan, &wg)
		go handleRepos(ctx, client, reposChan, forksChan, &wg)
		go handleForks(ctx, client, forksChan, &wg)
		wg.Wait()

		// ¯\_(ツ)_/¯
		logrus.Info("all we do is win, win, win, no matter what")

		return nil
	}

	// Run our program.
	p.Run()
}

func getSearchResults(ctx context.Context, client *github.Client, page int, repos chan<- github.Repository, wg *sync.WaitGroup) {
	defer wg.Done()
	defer close(repos)
	opts := &github.SearchOptions{Sort: "indexed", Order: "asc", ListOptions: github.ListOptions{Page: page}}
	for i := 0; i < 2; i++ {
		results, resp, err := client.Search.Code(ctx, "github.com/golang/lint/golint filename:.travis.yml", opts)
		if _, ok := err.(*github.RateLimitError); ok {
			logrus.Fatal("hit rate limit")
			return
		}
		if err != nil {
			logrus.Fatal(err)
			return
		}
		// logrus.Infof("Total Search Results: %v", results.GetTotal())

		for _, cr := range results.CodeResults {
			repo := cr.GetRepository()
			fileContent, _, _, err := getFileContent(ctx, client, repo)
			if _, ok := err.(*github.RateLimitError); ok {
				logrus.Fatal("hit rate limit")
				return
			}
			if err != nil {
				continue
			}

			// check file contains github.com/golang/lint/golint
			if strings.Contains(fileContent, "github.com/golang/lint/golint") == false {
				continue
			}

			// check for a valid go version
			if ok := checkValidGoVersion([]byte(fileContent)); ok == false {
				continue
			}

			// check that golint-fixer hasn't already opened a PR
			openPRsFiltered, _, err := client.PullRequests.List(ctx, repo.GetOwner().GetLogin(), repo.GetName(), &github.PullRequestListOptions{State: "all", Head: "golint-fixer:master"})
			if _, ok := err.(*github.RateLimitError); ok {
				logrus.Fatal("hit rate limit")
				return
			}
			if err != nil {
				continue
			}

			// if PR has not already been opened/closed
			if len(openPRsFiltered) == 0 {
				repos <- *cr.GetRepository()
				logrus.Debugf("sent %s to be forked", repo.GetName())
			}
		}

		if resp.NextPage == 0 {
			break
		}

		opts.Page = resp.NextPage
		logrus.Debugf("Going to page %d", resp.NextPage)
		// to stay within search rate limit
		time.Sleep(2 * time.Second)
	}
	logrus.Debug("Done searching!")
	return
}

func checkValidGoVersion(travisFile []byte) bool {
	travisYML := Travis{}

	err := yaml.Unmarshal(travisFile, &travisYML)
	if err != nil {
		logrus.Errorf("error: %v", err)
		return false
	}

	for _, value := range travisYML.GoVersions {
		ver, err := semver.ParseTolerant(value)
		if err != nil {
			return false
		}
		if validGoVersion.GT(ver) {
			return false
		}
	}
	return true
}

func handleRepos(ctx context.Context, client *github.Client, repos <-chan github.Repository, forks chan<- github.Repository, wg *sync.WaitGroup) {
	defer wg.Done()
	defer close(forks)
	var wg2 sync.WaitGroup

	// create the fork
	for repo := range repos {
		wg2.Add(1)
		logrus.Debugf("creating fork for %s", repo.GetName())
		go createFork(ctx, client, repo, forks, &wg2)
	}

	wg2.Wait()
}

func handleForks(ctx context.Context, client *github.Client, forks <-chan github.Repository, wg *sync.WaitGroup) {
	defer wg.Done()

	for repo := range forks {
		// verify that the forked repo is fully created
		for i := 0; i < 4; i++ {
			result, _, err := client.Repositories.Get(ctx, repo.GetOwner().GetLogin(), repo.GetName())
			if _, ok := err.(*github.RateLimitError); ok {
				logrus.Fatal("hit rate limit")
				return
			}
			if err == nil {
				repo = *result
				break
			}
			logrus.Debugf("Sleeping on %s", repo.GetName())
			time.Sleep(30 * time.Second)
		}

		// get .travis.yml
		fileContent, file, repo, err := getFileContent(ctx, client, &repo)
		if err != nil {
			logrus.Fatal(err)
			break
		}

		// create commit
		fixedFile := fixFile(fileContent)
		commitMessage := new(string)
		*commitMessage = "Fix golint import path"
		SHA := file.GetSHA()
		opts := github.RepositoryContentFileOptions{Content: []byte(fixedFile), Message: commitMessage, SHA: &SHA}
		err = updateFile(ctx, client, *repo, opts)
		if err != nil {
			logrus.Fatal(err)
			break
		}

		// create PR
		err = createPullRequest(ctx, client, *repo)
		if err != nil {
			logrus.Fatal(err)
			break
		}
	}
}

func createFork(ctx context.Context, client *github.Client, repo github.Repository, forks chan<- github.Repository, wg *sync.WaitGroup) {
	defer wg.Done()

	result, _, err := client.Repositories.CreateFork(ctx, repo.GetOwner().GetLogin(), repo.GetName(), new(github.RepositoryCreateForkOptions))
	if _, ok := err.(*github.RateLimitError); ok {
		logrus.Fatal("hit rate limit")
		return
	}
	if _, ok := err.(*github.AcceptedError); ok {
		logrus.Debugf("Sleeping after fork creation of %s", repo.GetName())
		time.Sleep(2 * time.Second)
		forks <- *result
		return
	}
	if err != nil {
		logrus.Fatal(err)
		return
	}
}

func getFileContent(ctx context.Context, client *github.Client, repo *github.Repository) (string, *github.RepositoryContent, *github.Repository, error) {
	file, _, _, err := client.Repositories.GetContents(ctx, repo.GetOwner().GetLogin(), repo.GetName(), ".travis.yml", new(github.RepositoryContentGetOptions))
	if _, ok := err.(*github.RateLimitError); ok {
		logrus.Fatal("hit rate limit")
		return "", nil, nil, err
	}
	if err != nil {
		return "", nil, nil, err
	}
	fileContent, err := file.GetContent()
	if err != nil {
		logrus.Debugf("unable to get file content: %v", err)
		fileContent = ""
	}
	return fileContent, file, repo, nil
}

func fixFile(fileContent string) string {
	result := strings.Replace(fileContent, "github.com/golang/lint/golint", "golang.org/x/lint/golint", -1)
	return result
}

func updateFile(ctx context.Context, client *github.Client, repo github.Repository, opts github.RepositoryContentFileOptions) error {
	_, _, err := client.Repositories.UpdateFile(ctx, repo.GetOwner().GetLogin(), repo.GetName(), ".travis.yml", &opts)
	if _, ok := err.(*github.RateLimitError); ok {
		logrus.Fatal("hit rate limit")
		return err
	}
	if err != nil {
		logrus.Debug("Failed to create commit")
		logrus.Fatal(err)
		return err
	}
	return nil
}

func createPullRequest(ctx context.Context, client *github.Client, repo github.Repository) error {
	parentRepo := repo.GetParent()
	title := "Fix golint import path"
	head := repo.GetOwner().GetLogin() + ":master"
	base := "master"
	canEdit := true
	opts := &github.NewPullRequest{}
	opts.Title = &title
	opts.Head = &head
	opts.Base = &base
	opts.MaintainerCanModify = &canEdit

	_, _, err := client.PullRequests.Create(ctx, parentRepo.GetOwner().GetLogin(), parentRepo.GetName(), opts)
	if _, ok := err.(*github.RateLimitError); ok {
		logrus.Fatal("hit rate limit")
		return err
	}
	if err != nil {
		logrus.Debug("Failed to create PR")
		logrus.Fatal(err)
		return err
	}
	logrus.Infof("Created PR for %s", repo.GetName())
	return nil
}
