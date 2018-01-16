package main

import (
	"fmt"
	"io/ioutil"
	"log"

	"github.com/dgageot/getme/cache"
	"github.com/dgageot/getme/files"
	"github.com/dgageot/getme/tar"
	"github.com/dgageot/getme/urls"
	"github.com/dgageot/getme/zip"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
	"github.com/bndr/gojenkins"
	"time"
	"os"
)

var (
	force bool
)

func main() {
	var rootCmd = &cobra.Command{Use: "getme"}

	options := files.Options{}

	rootCmd.PersistentFlags().StringVar(&options.AuthToken, "authToken", "", "Api authentication token")
	rootCmd.PersistentFlags().StringVar(&options.AuthTokenEnvVariable, "authTokenEnvVariable", "", "Env variable containing an api authentication token")
	rootCmd.PersistentFlags().StringVar(&options.S3AccessKey, "s3AccessKey", "", "Amazon S3 access key")
	rootCmd.PersistentFlags().StringVar(&options.S3SecretKey, "s3SecretKey", "", "Amazon S3 secret key")
	rootCmd.PersistentFlags().StringVar(&options.Sha256, "sha256", "", "Checksum to check downloaded files")
	rootCmd.PersistentFlags().BoolVar(&force, "force", false, "Force download")

	rootCmd.AddCommand(&cobra.Command{
		Use: "Download",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) < 1 {
				return errors.New("An url must be provided")
			}
			url := args[0]

			return Download(url, options)
		},
	})

	rootCmd.AddCommand(&cobra.Command{
		Use: "Copy",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) < 2 {
				return errors.New("An url and a destination must be provided")
			}
			url := args[0]
			destination := args[1]

			return Copy(url, options, destination)
		},
	})

	rootCmd.AddCommand(&cobra.Command{
		Use:     "Extract",
		Aliases: []string{"Unzip", "UnzipSingleFile"},
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) < 2 {
				return errors.New("An url, a file name and a destination must be provided")
			}

			url := args[0]

			// All files
			if len(args) == 2 {
				destinationFolder := args[1]

				return Extract(url, options, destinationFolder)
			}

			// Some files
			extractedFiles := []files.ExtractedFile{}
			for i := 1; i < len(args); i += 2 {
				extractedFiles = append(extractedFiles, files.ExtractedFile{
					Source:      args[i],
					Destination: args[i+1],
				})
			}

			return ExtractFiles(url, options, extractedFiles)
		},
	})

	rootCmd.AddCommand(&cobra.Command{
		Use: "Pinata",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) != 6 {
				return errors.New("A commit and platform must be provided")
			}
			jenkins := args[0]
			user := args[1]
			token := args[2]
			bucket := args[3]
			commit := args[4]
			platform := args[5]

			return Pinata(jenkins, user, token, bucket, commit, platform, options)
		},
	})

	if err := rootCmd.Execute(); err != nil {
		log.Fatal(err)
	}
}

// Download retrieves an url from the cache or download it if it's absent.
// Then print the path to that file to stdout.
func Pinata(jenkins, user, token, bucket, commit, platform string, options files.Options) error {
	binary := fmt.Sprintf("https://storage.googleapis.com/%s/%s/docker-for-%s.iso.tgz", bucket, commit, platform)
	err := Download(binary, options)
	if err != nil {
		log.SetOutput(os.Stdout)
		log.Println("Trigger jenkins build")
		jenkins := gojenkins.CreateJenkins(nil, jenkins, user, token)
		_, err := jenkins.Init()
		if err != nil {
			return err
		}

		job, err := jenkins.GetJob(fmt.Sprintf("pinata-%s-iso", platform))
		if err != nil {
			return err
		}

		taskId, err := job.InvokeSimple(map[string]string{
			"COMMIT_ID": commit,
		})
		if err != nil {
			return err
		}

		log.Println("Waiting for queue")
		for {
			queue, err := jenkins.GetQueue()
			if err != nil {
				return err
			}
			task := queue.GetTaskById(taskId)
			if task == nil {
				break
			}
			time.Sleep(time.Second)
		}

		ids, err := job.GetAllBuildIds()
		if err != nil {
			return err
		}
		for _, id := range ids {
			build, err := job.GetBuild(id.Number)
			if err != nil {
				return err
			}
			if build.GetParameters()[0].Value == commit {
				for {
					if !build.IsRunning() {
						break
					}
					log.Println("Job is running, waiting...")
					time.Sleep(5 * time.Second)
					build, err = job.GetBuild(id.Number)
					if err != nil {
						return err
					}
				}
				if build.IsGood() {
					return Download(binary, options)
				}
				return fmt.Errorf("Build failed")
			}
		}
		return fmt.Errorf("Build not found")
	}
	return nil
}

// Download retrieves an url from the cache or download it if it's absent.
// Then print the path to that file to stdout.
func Download(url string, options files.Options) error {
	// Discard all the logs. We only want to output the path to the file
	log.SetOutput(ioutil.Discard)

	source, err := cache.Download(url, options, force)
	if err != nil {
		return err
	}

	fmt.Println(source)

	return nil
}

// Copy retrieves an url from the cache or download it if it's absent.
// Then it copies the file to a destination path.
func Copy(url string, options files.Options, destination string) error {
	// Discard all the logs. We only want to output the path to the file
	if destination == "-" {
		log.SetOutput(ioutil.Discard)
	}

	source, err := cache.Download(url, options, force)
	if err != nil {
		return err
	}

	log.Println("Copy", url, "to", destination)

	return files.Copy(source, destination)
}

// Extract retrieves an url from the cache or download it if it's absent.
// Then it unzips the file to a destination directory.
func Extract(url string, options files.Options, destinationDirectory string) error {
	source, err := cache.Download(url, options, force)
	if err != nil {
		return err
	}

	log.Println("Extract", url, "to", destinationDirectory)

	if urls.IsZipArchive(url) {
		return zip.Extract(source, destinationDirectory)
	}
	if urls.IsTarArchive(url) {
		return tar.Extract(url, source, destinationDirectory)
	}

	return errors.New("Unsupported archive: " + source)
}

// ExtractFiles retrieves an url from the cache or download it if it's absent.
// Then it unzips some files from that zip to a destination path.
func ExtractFiles(url string, options files.Options, files []files.ExtractedFile) error {
	source, err := cache.Download(url, options, force)
	if err != nil {
		return err
	}

	for _, file := range files {
		log.Println("Extract", file.Source, "from", url, "to", file.Destination)
	}

	if urls.IsZipArchive(url) {
		return zip.ExtractFiles(source, files)
	}
	if urls.IsTarArchive(url) {
		return tar.ExtractFiles(url, source, files)
	}

	return errors.New("Unsupported archive: " + source)
}
