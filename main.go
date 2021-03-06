package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"strings"

	"github.com/alecthomas/template"
	"github.com/blang/semver"
	"github.com/docker/docker/client"
	"github.com/srizzling/aquarium/version"
	yaml "gopkg.in/yaml.v1"
)

type gitBranch struct {
	Name string
}

type gitCommit struct {
	ShortHash string
	LongHash  string
}

type gitTag struct {
	Major  string
	Minor  string
	Patch  string
	Raw    string
	SemVer bool
}

type aqTemplate struct {
	Tag    *gitTag
	Commit *gitCommit
	Branch *gitBranch
}

type aqConfig struct {
	TagFormat   []string `yaml:"tag_format"`
	LabelFormat []string `yaml:"label_format"`
	ImageNames  []string `yaml:"image_names"`
}

var (
	versionFlag  bool
	outputFormat string
	imgID        string
)

const banner = `
aquarium - tag docker images with git metadata
Version: %s
GitCommitSHA: %s

`

func init() {
	flag.StringVar(&imgID, "imgID", "", "The Id of the image to tag")
	flag.StringVar(&outputFormat, "output", "json", "The formatting style for the command output allowed values: [json, text]")
	flag.BoolVar(&versionFlag, "v", false, "print version and exit")

	flag.Usage = func() {
		fmt.Fprint(os.Stderr, fmt.Sprintf(banner, version.Version, version.GitCommitSHA))
		flag.PrintDefaults()
	}

	flag.Parse()

	if versionFlag {
		fmt.Printf(banner, version.Version, version.GitCommitSHA)
		os.Exit(0)
	}

	if imgID == "" {
		usageAndExit("Image id cannot be empty", 1)
	}

	if outputFormat != "json" && outputFormat != "text" {
		usageAndExit("OutputFormat not accepte	d", 1)
	}
}

func main() {
	config := aqConfig{}
	data, err := ioutil.ReadFile(".aquarium.yml")
	if err != nil {
		panic(err)
	}

	err = yaml.Unmarshal(data, &config)
	if err != nil {
		panic(err)
	}

	tmplData, err := getGitInfo()
	if err != nil {
		panic(err)
	}

	docker, err := client.NewEnvClient()
	if err != nil {
		panic(err)
	}

	var taggedImgs []string
	for _, name := range config.ImageNames {
		dockerTags, err := setTag(name, tmplData, config.TagFormat, docker)
		if err != nil {
			panic(err)
		}
		taggedImgs = append(taggedImgs, dockerTags...)
	}

	printImgs(taggedImgs)
}

func printImgs(taggedImgs []string) {
	if outputFormat == "text" {
		for _, img := range taggedImgs {
			fmt.Printf("%s\n", img)
		}
	} else if outputFormat == "json" {
		var jsonReturn = struct {
			Images []string `json:"images"`
		}{
			taggedImgs,
		}

		json, err := json.Marshal(jsonReturn)
		if err != nil {
			panic(err)
		}
		fmt.Printf("%s", json)
	}
}

func setTag(name string, tmplData *aqTemplate, tagFormats []string, docker *client.Client) (images []string, err error) {
	for _, tagTemplate := range tagFormats {
		t := template.Must(template.New("tag_template").Parse(tagTemplate))
		buf := new(bytes.Buffer)
		err := t.Execute(buf, tmplData)
		if err != nil {
			return nil, err
		}

		imgName := fmt.Sprintf("%s:%s", name, buf.String())
		err = docker.ImageTag(context.Background(), imgID, imgName)
		if err != nil {
			return nil, err
		}
		images = append(images, imgName)
	}
	return images, nil

}

func runGit(args ...string) (string, error) {
	var cmd = exec.Command("git", args...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", errors.New(stderr.String())
	}
	return stdout.String(), nil
}

func getGitInfo() (*aqTemplate, error) {
	tag, err := getTag()
	if err != nil {
		return nil, err
	}

	commit, err := getCommit()
	if err != nil {
		return nil, err
	}

	branch, err := getBranch()
	if err != nil {
		return nil, err
	}

	gitTmpl := &aqTemplate{
		Tag:    tag,
		Branch: branch,
		Commit: commit,
	}

	return gitTmpl, nil
}

// getTag tries to imitate `git describe --tags` command to retreive the tag on the HEAD
func getTag() (*gitTag, error) {
	raw, err := runGit("describe", "--tags", "--abbrev=0")
	if err != nil {
		return nil, err
	}
	tag := strings.TrimSpace(raw)

	// Check if tag is semver compliant
	// does the tag start with v? strip it
	tag = strings.TrimPrefix(tag, "v")

	v, err := semver.Make(tag)
	if err != nil {
		// well the tag isn't semver compliant.. so lets just return the raw value
		return &gitTag{
			Raw:    tag,
			SemVer: false,
		}, nil
	}

	// unfourently git describe doesn't return a semver compliant tag
	// so lets just move it to build information
	return &gitTag{
		Major:  fmt.Sprint(v.Major),
		Minor:  fmt.Sprint(v.Minor),
		Patch:  fmt.Sprint(v.Patch),
		Raw:    tag,
		SemVer: true,
	}, nil
}

func getCommit() (*gitCommit, error) {
	longHash, err := runGit("rev-parse", "HEAD")
	if err != nil {
		return nil, err
	}

	shortHash, err := runGit("rev-parse", "--short", "HEAD")
	if err != nil {
		return nil, err
	}

	return &gitCommit{
		LongHash:  strings.TrimSpace(longHash),
		ShortHash: strings.TrimSpace(shortHash),
	}, nil
}

func getBranch() (*gitBranch, error) {
	name, err := runGit("rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return nil, err
	}
	return &gitBranch{
		Name: strings.TrimSpace(name),
	}, nil
}

func usageAndExit(message string, exitCode int) {
	if message != "" {
		fmt.Fprintf(os.Stderr, message)
		fmt.Fprintf(os.Stderr, "\n\n")
	}
	flag.Usage()
	fmt.Fprintf(os.Stderr, "\n")
	os.Exit(exitCode)
}
