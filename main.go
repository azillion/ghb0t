package main

import (
	"context"
	"flag"
	"fmt"
	"github.com/azillion/ghb0t/version"
	"github.com/genuinetools/pkg/cli"
	"github.com/google/go-github/github"
	"github.com/gregjones/httpcache/diskcache"
	"github.com/sirupsen/logrus"
	"golang.org/x/oauth2"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

var (
	token    string
	interval time.Duration
	enturl   string

	lastChecked time.Time

	debug       bool
	shouldCache bool
	cache       *diskcache.Cache
)

func main() {
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
		//var repos []string
		reposChan := make(chan github.Repository)
		done := make(chan bool)

		go getSearchResults(ctx, client, 1, reposChan)
		go handleRepos(ctx, client, reposChan, done)
		//repos = append(repos, <-ch)
		for x := range done {
			if x == true {
				fmt.Println("Created a fork, committed the change and opened the pull request")
			}
		}
		//fmt.Println(results.GetIncompleteResults())
		//fmt.Println(results.GetTotal())
		//fmt.Println(results.Total)
		//fmt.Println(results)

		return nil
	}

	// Run our program.
	p.Run()
}

func getSearchResults(ctx context.Context, client *github.Client, page int, repos chan github.Repository) {
	opts := &github.SearchOptions{Sort: "indexed", Order: "asc", ListOptions: github.ListOptions{Page: page}}
	for {
		//if resp, ok := cache.Get(strconv.Itoa(page)); shouldCache && ok {
		//
		//}
		results, resp, err := client.Search.Code(ctx, "github.com/golang/lint/golint filename:.travis.yml", opts)
		if _, ok := err.(*github.RateLimitError); ok {
			logrus.Fatal("hit rate limit")
			close(repos)
		}
		if err != nil {
			logrus.Fatal(err)
			close(repos)
		}

		//for _, value := range results.CodeResults {
		for _, value := range results.CodeResults[:1] {
			repos <- *value.GetRepository()
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

func handleRepos(ctx context.Context, client *github.Client, repos chan github.Repository, done chan bool) {
	forks := make(chan github.Repository)
	for repo := range repos {
		go createFork(ctx, client, repo, forks)
	}
	for repo := range forks {
		//file, repo, err := getFileContent(ctx, client, &repo)
		//if err != nil {
		//	logrus.Fatal(err)
		//	close(done)
		//	break
		//}
		//
		//fixedFile, err := fixFile(file)
		//if err != nil {
		//	logrus.Fatal(err)
		//	close(done)
		//	break
		//}
		//
		//commitMessage := new(string)
		//*commitMessage = "Fix golint import path"
		//SHA := file.GetSHA()
		//opts := github.RepositoryContentFileOptions{Content: fixedFile, Message: commitMessage, SHA: &SHA}
		//err = updateFile(ctx, client, repo, &opts)
		//if err != nil {
		//	logrus.Fatal(err)
		//	close(done)
		//	break
		//}
		err := createPullRequest(ctx, client, &repo)
		if err != nil {
			logrus.Fatal(err)
			close(done)
			break
		}
		done <- true
	}
	close(done)
}

func createFork(ctx context.Context, client *github.Client, repo github.Repository, forks chan github.Repository) {
	result, _, err := client.Repositories.CreateFork(ctx, repo.GetOwner().GetLogin(), repo.GetName(), new(github.RepositoryCreateForkOptions))
	if _, ok := err.(*github.RateLimitError); ok {
		logrus.Fatal("hit rate limit")
		return
	}
	if _, ok := err.(*github.AcceptedError); ok {
		time.Sleep(30 * time.Second)
		forks <- *result
		return
	}
	if err != nil {
		logrus.Fatal(err)
		return
	}
}

func getFileContent(ctx context.Context, client *github.Client, repo *github.Repository) (*github.RepositoryContent, *github.Repository, error) {
	// first check that the repo exists
	for i := 0; i < 4; i++ {
		result, _, err := client.Repositories.Get(ctx, repo.GetOwner().GetLogin(), repo.GetName())
		if _, ok := err.(*github.RateLimitError); ok {
			logrus.Fatal("hit rate limit")
			return nil, nil, err
		}
		if err == nil {
			repo = result
			break
		}
		time.Sleep(30 * time.Second)
	}

	file, _, _, err := client.Repositories.GetContents(ctx, repo.GetOwner().GetLogin(), repo.GetName(), ".travis.yml", new(github.RepositoryContentGetOptions))
	if _, ok := err.(*github.RateLimitError); ok {
		logrus.Fatal("hit rate limit")
		return nil, nil, err
	}
	if err != nil {
		logrus.Fatal(err)
		return nil, nil, err
	}
	return file, repo, nil
}

func fixFile(file *github.RepositoryContent) ([]byte, error) {
	fileContent, err := file.GetContent()
	if err != nil {
		fmt.Println("unable to get file content:", err)
		return []byte(""), err
	}
	//decoded, err := base64.StdEncoding.DecodeString(fileContent)
	//if err != nil {
	//	fmt.Println("decode error:", err)
	//	return []byte(""), err
	//}
	result := strings.Replace(string(fileContent), "github.com/golang/lint/golint", "golang.org/x/lint/golint", -1)

	return []byte(result), nil
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

	_, _, err := client.PullRequests.Create(ctx, parentRepo.GetOwner().GetLogin(), parentRepo.GetName(),  opts)
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
