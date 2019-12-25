// Copyright (c) 2019 Cisco Systems, Inc.
//
// SPDX-License-Identifier: Apache-2.0
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at:
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Utility to cleanup old CI images from DockerHub
package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
)

type imageCleanupCmd struct {
	cobra.Command

	imageMinAge time.Duration
	images      []string
	tags        []string
	repo        string
	count       int
}

func main() {
	var rootCmd = &imageCleanupCmd{
	}
	rootCmd.Short = "Utility to cleanup old CI images from DockerHub"
	rootCmd.Run = func(cmd *cobra.Command, args []string) {
		if len(args) > 0 {
			rootCmd.images = args
		}
		cleanupImages(rootCmd)
	}
	rootCmd.Args = func(cmd *cobra.Command, args []string) error {
		return nil
	}
	rootCmd.Flags().StringVarP(&rootCmd.repo, "repo", "r", "", "Repository to cleanup")
	rootCmd.Flags().StringSliceVarP(&rootCmd.images, "images", "i", []string{}, "Images to remove")
	rootCmd.Flags().StringSliceVarP(&rootCmd.tags, "tags", "t", []string{}, "Tags to remove")
	rootCmd.Flags().IntVarP(&rootCmd.count, "count", "", 10, "Remove only count of tags")
	rootCmd.Flags().DurationVarP(&rootCmd.imageMinAge, "minage", "", 7*24*time.Hour,
		"Minimum age of tag to delete")

	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}

func cleanupImages(cmd *imageCleanupCmd) {
	tagsCh := make(chan string)
	errCh := make(chan error)
	go listImageTags(cmd, tagsCh, errCh)
	for {
		select {
		case tag, ok := <-tagsCh:
			if !ok {
				return
			}
			logrus.Infof("Removing images for tag %s:", tag)
			for _, image := range cmd.images {
				logrus.Infof("%s ", image)
			}
		case err := <-errCh:
			logrus.Errorf("Error getting tag list: %v", err)
			return
		}
	}
}

type tagInfo struct {
	Name        string `json:"Name"`
	LastUpdated string `json:"last_updated"`
}

type tagList struct {
	Next     string     `json:"Next"`
	Previous string     `json:"Previous"`
	Results  []*tagInfo `json:"Results"`
	Count    int        `json:"count"`
}

func listImageTags(cmd *imageCleanupCmd, tagsCh chan string, errCh chan error) {
	defer close(tagsCh)
	defer close(errCh)

	tagsFound := 0
	tagsUrl := fmt.Sprintf("https://hub.docker.com/v2/repositories/%s/%s/tags", cmd.repo, cmd.images[0])
	for ; tagsUrl != "null"; {
		resp, err := http.Get(tagsUrl)
		if err != nil {
			errCh <- err
			return
		}
		decoder := json.NewDecoder(resp.Body)
		var tagPage tagList
		if err := decoder.Decode(&tagPage); err != nil {
			errCh <- err
			return
		}
		for _, jt := range tagPage.Results {
			if lu, err := time.Parse(time.RFC3339, jt.LastUpdated); err == nil && time.Since(lu) < cmd.imageMinAge {
				continue
			}
			//logrus.Infof("tag: %v", jt)
			tagsCh <- jt.Name
			tagsFound++
			if tagsFound >= cmd.count {
				return
			}
		}
		tagsUrl = tagPage.Next
		//logrus.Infof("tagsUrl: %s", tagsUrl)
	}
}
