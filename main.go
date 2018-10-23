package main

import (
	"context"
	"flag"
	"fmt"
	"github.com/azillion/ghb0t/version"
	"github.com/blang/semver"
	"github.com/genuinetools/pkg/cli"
	"github.com/google/go-github/github"
	"github.com/gregjones/httpcache/diskcache"
	"github.com/sirupsen/logrus"
	"golang.org/x/oauth2"
	"gopkg.in/yaml.v2"
	"log"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"
)

type Travis struct {
	GoVersions []string `yaml:"go,flow"`
}

var (
	token    string
	interval time.Duration
	enturl   string

	lastChecked time.Time

	debug          bool
	shouldCache    bool
	cache          *diskcache.Cache
	validGoVersion semver.Version
)

func init() {
	v, err := semver.ParseTolerant("1.9")
	if err != nil {
		logrus.Fatal(err)
	}
	validGoVersion = v
}

func main() {
	// TODO: Pass vars instead of pointers for the repos. Most likely the issue
	// Create a new cli program.
	p := cli.NewProgram()
	p.Name = "ghb0t"
	p.Description = "A GitHub Bot to search all github repos for `github.com/golang/lint/golint import`"

	// Set the GitCommit and Version.
	p.GitCommit = version.GITCOMMIT
	p.Version = version.VERSION

	// Setup the global flags.
	p.FlagSet = flag.NewFlagSet("global", flag.ExitOnError)
	p.FlagSet.StringVar(&token, "token", os.Getenv("GITHUB_TOKEN"), "GitHub API token (or env var GITHUB_TOKEN)")
	p.FlagSet.DurationVar(&interval, "interval", 30*time.Second, "check interval (ex. 5ms, 10s, 1m, 3h)")
	p.FlagSet.StringVar(&enturl, "url", "", "Connect to a specific GitHub server, provide full API URL (ex. https://github.example.com/api/v3/)")

	p.FlagSet.BoolVar(&debug, "d", false, "enable debug logging")
	p.FlagSet.BoolVar(&shouldCache, "c", true, "enable response caching")

	// Set the before function.
	p.Before = func(ctx context.Context) error {
		// Set the log level.
		if debug {
			logrus.SetLevel(logrus.DebugLevel)
		}

		if token == "" {
			return fmt.Errorf("GitHub token cannot be empty")
		}

		if shouldCache {
			cache = diskcache.New(".search-cache")
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

		// TODO: Add routines
		reposChan := make(chan *github.Repository)
		done := make(chan bool)

		go getSearchResults(ctx, client, 1, reposChan)
		go handleRepos(ctx, client, reposChan, done)
		for range reposChan {
			fmt.Println("hep")
		}
		fmt.Println("make it here")
		for range done {
			fmt.Println("hep2")
		}

		return nil
	}

	// Run our program.
	p.Run()
}

func getSearchResults(ctx context.Context, client *github.Client, page int, repos chan<- *github.Repository) {
	opts := &github.SearchOptions{Sort: "indexed", Order: "asc", ListOptions: github.ListOptions{Page: page}}
	for {
		results, resp, err := client.Search.Code(ctx, "github.com/golang/lint/golint filename:.travis.yml", opts)
		if _, ok := err.(*github.RateLimitError); ok {
			logrus.Fatal("hit rate limit")
			close(repos)
			return
		}
		if err != nil {
			logrus.Fatal(err)
			close(repos)
			return
		}

		for _, cr := range results.CodeResults {
			//for _, cr := range results.CodeResults[:1] {
			fileContent, _, _, err := getFileContent(ctx, client, cr.GetRepository())
			if _, ok := err.(*github.RateLimitError); ok {
				logrus.Fatal("hit rate limit")
				close(repos)
				return
			}
			if err != nil {
				continue
			}
			if ok := checkValidGoVersion([]byte(fileContent)); ok {
				fmt.Println("sending")
				repos <- cr.GetRepository()
				close(repos)
				fmt.Println("sent")
				return
			}
		}

		if resp.NextPage == 0 || results.GetIncompleteResults() == false {
			break
		}

		opts.Page = resp.NextPage
		time.Sleep(2050 * time.Millisecond)
		break // TODO: Remove when Ready
	}
	close(repos)
	return
}

func checkValidGoVersion(travisFile []byte) (bool) {
	travisYML := Travis{}

	err := yaml.Unmarshal(travisFile, &travisYML)
	if err != nil {
		log.Fatalf("error: %v", err)
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

func handleRepos(ctx context.Context, client *github.Client, repos <-chan *github.Repository, done chan<- bool) {
	forks := make(chan *github.Repository, 10)
	var wg sync.WaitGroup
	// create the fork
	for repo := range repos {
		wg.Add(1)
		fmt.Println("moved on")
		go createFork(ctx, client, repo, forks, wg)
	}

	// create the commit and PR
	for repo := range forks {
		fmt.Println("plz")
		// verify that the forked repo is fully created
		for i := 0; i < 4; i++ {
			result, _, err := client.Repositories.Get(ctx, repo.GetOwner().GetLogin(), repo.GetName())
			if _, ok := err.(*github.RateLimitError); ok {
				logrus.Fatal("hit rate limit")
				close(done)
				return
			}
			if err == nil {
				repo = result
				break
			}
			time.Sleep(30 * time.Second)
		}

		// get .travis.yml
		fileContent, file, repo, err := getFileContent(ctx, client, repo)
		if err != nil {
			logrus.Fatal(err)
			close(done)
			break
		}

		// create commit
		fixedFile := fixFile(fileContent)
		commitMessage := new(string)
		*commitMessage = "Fix golint import path"
		SHA := file.GetSHA()
		opts := github.RepositoryContentFileOptions{Content: []byte(fixedFile), Message: commitMessage, SHA: &SHA}
		err = updateFile(ctx, client, repo, &opts)
		if err != nil {
			logrus.Fatal(err)
			close(done)
			break
		}

		// create PR
		err = createPullRequest(ctx, client, repo)
		if err != nil {
			logrus.Fatal(err)
			close(done)
			break
		}
		done <- true
	}

	go func() {
		wg.Wait()
		close(forks)
		close(done)
	}()
}

func createFork(ctx context.Context, client *github.Client, repo *github.Repository, forks chan<- *github.Repository, wg sync.WaitGroup) {
	defer wg.Done()
	result, _, err := client.Repositories.CreateFork(ctx, repo.GetOwner().GetLogin(), repo.GetName(), new(github.RepositoryCreateForkOptions))
	if _, ok := err.(*github.RateLimitError); ok {
		logrus.Fatal("hit rate limit")
		close(forks)
		return
	}
	if _, ok := err.(*github.AcceptedError); ok {
		time.Sleep(30 * time.Second)
		forks <- result
		return
	}
	if err != nil {
		logrus.Fatal(err)
		close(forks)
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
		fmt.Println("unable to get file content:", err)
		fileContent = ""
	}
	return fileContent, file, repo, nil
}

func fixFile(fileContent string) string {
	result := strings.Replace(fileContent, "github.com/golang/lint/golint", "golang.org/x/lint/golint", -1)
	return result
}

func updateFile(ctx context.Context, client *github.Client, repo *github.Repository, opts *github.RepositoryContentFileOptions) error {
	_, _, err := client.Repositories.UpdateFile(ctx, repo.GetOwner().GetLogin(), repo.GetName(), ".travis.yml", opts)
	if _, ok := err.(*github.RateLimitError); ok {
		logrus.Fatal("hit rate limit")
		return err
	}
	if err != nil {
		logrus.Fatal(err)
		return err
	}
	return nil
}

func createPullRequest(ctx context.Context, client *github.Client, repo *github.Repository) error {
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
		logrus.Fatal(err)
		return err
	}
	return nil
}
