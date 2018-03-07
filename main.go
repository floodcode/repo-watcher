package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/fsnotify/fsnotify"
)

type watcherConfig struct {
	ReposRoot   string `json:"reposRoot"`
	WatchPath   string `json:"watchPath"`
	WatchRegexp string `json:"watchRegexp"`
	Execute     string `json:"execute"`
}

var config watcherConfig

type logType string

const (
	logInfo  logType = "info"
	logError logType = "error"
)

var (
	reposMutex    *sync.Mutex
	reposWatcher  *fsnotify.Watcher
	watcher       *fsnotify.Watcher
	watcherRegexp *regexp.Regexp
)

var currentlyWatching map[string]bool

func main() {
	log(logInfo, "Starting repos watcher...")

	var err error
	reposWatcher, err = fsnotify.NewWatcher()
	checkError(err)
	defer reposWatcher.Close()

	watcher, err = fsnotify.NewWatcher()
	checkError(err)
	defer watcher.Close()

	reposMutex = &sync.Mutex{}
	currentlyWatching = map[string]bool{}

	configData, err := ioutil.ReadFile("config.json")
	checkError(err)

	err = json.Unmarshal(configData, &config)
	checkError(err)

	watcherRegexp, err = regexp.Compile(config.WatchRegexp)
	checkError(err)

	watchRepos(config.ReposRoot)
}

func checkError(e error) {
	if e != nil {
		log(logError, e.Error())
	}
}

func watchRepos(reposRoot string) {
	info, err := os.Stat(reposRoot)
	if os.IsNotExist(err) {
		panic("repos root does not exists: " + reposRoot)
	}

	if !info.IsDir() {
		panic("specified repos root is not a directory: " + reposRoot)
	}

	reposDone := make(chan bool)
	go func() {
		for {
			select {
			case event := <-reposWatcher.Events:
				processReposEvent(event)
			}
		}
	}()

	err = reposWatcher.Add(reposRoot)
	if err != nil {
		log(logError, err.Error())
		return
	}

	done := make(chan bool)
	go func() {
		for {
			select {
			case event := <-watcher.Events:
				processEvent(event)
			}
		}
	}()

	files, err := ioutil.ReadDir(reposRoot)
	for _, file := range files {
		addRepo(reposRoot + "/" + file.Name())
	}

	<-done
	<-reposDone
}

func processReposEvent(event fsnotify.Event) {
	if event.Op&fsnotify.Create == fsnotify.Create {
		addRepo(event.Name)
	} else if event.Op&fsnotify.Chmod == fsnotify.Chmod {
		addRepo(event.Name)
	} else if event.Op&fsnotify.Rename == fsnotify.Rename {
		removeRepo(event.Name)
	} else if event.Op&fsnotify.Remove == fsnotify.Remove {
		removeRepo(event.Name)
	}
}

func processEvent(event fsnotify.Event) {
	separator := string(os.PathSeparator)
	if info, err := os.Stat(event.Name); os.IsNotExist(err) || info.IsDir() {
		return
	}

	dir, baseName := filepath.Split(event.Name)
	if !watcherRegexp.Match([]byte(baseName)) {
		return
	}

	reposRootParts := filterEmptyParts(strings.Split(config.ReposRoot, separator))
	dirPathParts := filterEmptyParts(strings.Split(dir, separator))

	repoPath := strings.Join(dirPathParts[:len(reposRootParts)+1], separator)

	execMessage := fmt.Sprintf("[RepoWatch] Executing \"%s\" in \"%s\"", config.Execute, repoPath)
	log(logInfo, execMessage)

	cmd := exec.Command("sh", "-c", config.Execute)
	cmd.Dir = repoPath

	output, err := cmd.Output()
	if err != nil {
		log(logError, err.Error())
		return
	}

	log(logInfo, string(output))
}

func filterEmptyParts(elements []string) []string {
	result := []string{}

	for _, element := range elements {
		if len(element) > 0 {
			result = append(result, element)
		}
	}

	return result
}

func addRepo(path string) {
	cleanPath := filepath.Clean(path)
	if info, err := os.Stat(cleanPath); err != nil || !info.IsDir() {
		return
	}

	targetPath := filepath.Clean(cleanPath + "/" + config.WatchPath)
	if info, err := os.Stat(targetPath); err != nil || !info.IsDir() {
		return
	}

	reposMutex.Lock()
	if _, ok := currentlyWatching[cleanPath]; !ok {
		log(logInfo, "[RepoWatch] Adding repo: "+cleanPath)

		watcher.Add(targetPath)
		currentlyWatching[cleanPath] = true
	}
	reposMutex.Unlock()
}

func removeRepo(path string) {
	cleanPath := filepath.Clean(path)
	targetPath := filepath.Clean(cleanPath + "/" + config.WatchPath)

	reposMutex.Lock()
	if _, ok := currentlyWatching[cleanPath]; ok {
		log(logInfo, "[RepoWatch] Removing repo: "+cleanPath)

		watcher.Remove(targetPath)
		delete(currentlyWatching, cleanPath)
	}
	reposMutex.Unlock()
}

func log(kind logType, message string) {
	fmt.Printf("%s: %s\n", kind, message)

	logFilename := fmt.Sprintf("%s.log", kind)

	f, _ := os.OpenFile(logFilename, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	f.Write([]byte(message + "\n"))
	f.Close()
}
