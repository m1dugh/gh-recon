package ghrecon

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"regexp"
	"sync"

	"github.com/go-git/go-billy/v5"
	"github.com/go-git/go-billy/v5/memfs"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/storage/memory"
)

type User struct {
	Login    string `json:"login"`
	Id       int    `json:"id"`
	Type     string `json:"type"`
	ReposUrl string `json:"repos_url"`
}

type Repository struct {
	Id       int    `json:"id"`
	Owner    User   `json:"owner"`
	Name     string `json:"name"`
	FullName string `json:"full_name"`
	Url      string `json:"clone_url"`
}

type TargetFile struct {
	Filename string
	Data     []byte
	Repo     *Repository
}

func _GetRepositories(url string) []Repository {
	var repos []Repository
	response, err := http.Get(url)
	if err != nil {
		log.Fatal("could not fetch repos")
	} else if response.StatusCode == 403 {
		log.Fatal("rate limited by github")
	}
	defer response.Body.Close()

	body, _ := ioutil.ReadAll(response.Body)

	err = json.Unmarshal(body, &repos)
	if err != nil {
		log.Fatal("could not parse json response for repos")
	}

	return repos
}

func (u *User) GetRepositories() []Repository {
	return _GetRepositories(_ExtractUrl(u.ReposUrl))
}

func (org *Organization) GetRepositories() []Repository {
	return _GetRepositories(_ExtractUrl(org.ReposUrl))
}

func ParseRepository(fs *billy.Filesystem, repo *Repository) []TargetFile {
	storer := memory.NewStorage()

	if _, err := git.Clone(storer, *fs, &git.CloneOptions{
		URL: repo.Url,
	}); err != nil {
		log.Fatal(fmt.Sprintf("could not fetch %s", repo.FullName))
	}

	names := make(map[plumbing.Hash]string)
	for _, binTree := range storer.Trees {
		tree, _ := object.DecodeTree(storer, binTree)

		for _, entry := range tree.Entries {
			if entry.Mode == filemode.Regular {

				names[entry.Hash] = entry.Name
			}
		}
	}

	targets := make([]TargetFile, len(names))
	i := 0
	for _, obj := range storer.Blobs {

		reader, _ := obj.Reader()
		data, _ := ioutil.ReadAll(reader)
		targets[i] = TargetFile{
			names[obj.Hash()],
			data,
			repo,
		}
		i++
	}

	return targets
}

type Organization struct {
	Id         int    `json:"id"`
	Login      string `json:"login"`
	ReposUrl   string `json:"repos_url"`
	MembersUrl string `json:"members_url"`
}

func _ExtractUrl(url string) string {
	urlPattern := regexp.MustCompile(`^https?://(\w+\.)+[a-z]{2,5}(/[^"'\s><\\\{\}]+)*`)
	return urlPattern.FindString(url)
}

func GetOrganization(url string) *Organization {
	response, err := http.Get(url)

	if err != nil {
		log.Fatal(fmt.Sprintf("could not get organization for url %s", url))
	} else if response.StatusCode == 403 {
		log.Fatal("could not fetch organization, rate limited by github")
	}
	defer response.Body.Close()

	body, _ := ioutil.ReadAll(response.Body)

	var org Organization
	if json.Unmarshal(body, &org) != nil {
		log.Fatal("could not parse response as json at get organization")
	}

	return &org
}

func (org *Organization) GetMembers() []User {
	response, err := http.Get(org.MembersUrl)

	fmt.Println("members url: ", _ExtractUrl(org.MembersUrl))

	if err != nil {
		log.Fatal(fmt.Sprintf("could not get members for url %s", org.MembersUrl))
	} else if response.StatusCode == 403 {
		log.Fatal("rate limited by github")
	}

	defer response.Body.Close()

	body, _ := ioutil.ReadAll(response.Body)

	var users []User
	if json.Unmarshal(body, &users) != nil {
		return make([]User, 0)
	}

	return users
}

func FullRecon(url string, hooks []func(TargetFile)) {

	org := GetOrganization(url)

	// get users in organization.

	users := org.GetMembers()

	// get all projects

	repos := org.GetRepositories()

	for _, user := range users {
		var userRepos []Repository = user.GetRepositories()

		for _, repo := range userRepos {
			if repo.Owner.Type == "Organization" && repo.Owner.Login == org.Login {
				continue
			}

			repos = append(repos, repo)
		}

	}

	// order repositories based on how likely they are to have sensitive informations

	// get all data in all repositories

	fs := memfs.New()
	wg := &sync.WaitGroup{}
	input := make(chan *Repository)
	for _, repo := range repos {
		wg.Add(1)
		go (func(fs *billy.Filesystem) {
			defer wg.Done()
			for _, file := range ParseRepository(fs, <-input) {
				for _, f := range hooks {
					f(file)
				}
			}
		})(&fs)

		input <- &repo
	}

	wg.Wait()
}
