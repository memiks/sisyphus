package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/boltdb/bolt"
	"github.com/fsnotify/fsnotify"
	log "github.com/sirupsen/logrus"
	"github.com/urfave/cli"

	"github.com/carlostrub/sisyphus"
)

var (
	version string
)

func main() {

	// Define App
	app := cli.NewApp()
	app.Name = "Sisyphus"
	app.Usage = "Intelligent Junk Mail Handler"
	app.UsageText = `
	Sisyphus applies artificial intelligence to filter Junk mail in an
	unobtrusive way. Both, classification and learning operate directly on
	the Maildir of a user in a fully transparent mode, without any need for
	configuration or active operation.`
	app.HelpName = "Intelligent Junk Mail Handler"
	app.Version = version
	app.Copyright = "(c) 2017, 2018, Carlo Strub. All rights reserved. This binary is licensed under a BSD 3-Clause License."
	app.Authors = []cli.Author{
		{
			Name:  "Carlo Strub",
			Email: "cs@carlostrub.ch",
		},
	}
	app.ExtraInfo = func() map[string]string {
		return map[string]string{
			"ENVIRONMENT VARIABLES": `For configuration, set the following environment
  variables:
  
  SISYPHUS_DIRS:     Comma-separated list of maildirs,
                     e.g. ./Maildir,/home/JohnDoe/Maildir

  SISYPHUS_DURATION: Interval between learning periods, e.g. 12h. Default is set to 24h.

  SISYPHUS_DRY_RUN : If set, sisyphus will not move any mails around.
			`,
		}
	}
	app.CustomAppHelpTemplate = `NAME:
  {{.Name}} - {{.Usage}}

USAGE:
  sisyphus {{if .VisibleFlags}}[FLAGS] {{end}}COMMAND{{if .VisibleFlags}}{{end}}
  {{.UsageText}}

COMMANDS:
  {{range .VisibleCommands}}{{join .Names ", "}}{{ "\t" }}{{.Usage}}
  {{end}}{{if .VisibleFlags}}
FLAGS:
  {{range .VisibleFlags}}{{.}}
  {{end}}{{end}}
{{range $key, $value := ExtraInfo}}
{{$key}}:
  {{$value}}
{{end}}VERSION:
  {{.Version}}

AUTHOR:{{range .Authors}}
  {{.}}{{end}}

COPYRIGHT:
  {{.Copyright}}
`

	app.Commands = []cli.Command{
		{
			Name:    "run",
			Aliases: []string{"u"},
			Usage:   "run sisyphus",
			Action: func(c *cli.Context) {

				fmt.Print(`


                                                                                           
               #####                                             
              #     # #  ####  #   # #####  #    # #    #  ####  
              #       # #       # #  #    # #    # #    # #      
               #####  #  ####    #   #    # ###### #    #  ####  
                    # #      #   #   #####  #    # #    #      # 
              #     # # #    #   #   #      #    # #    # #    # 
               #####  #  ####    #   #      #    #  ####   ####  

              by Carlo Strub <cs@carlostrub.ch>


`)

				maildirs := loadConfig()

				// Open all databases
				dbs, err := sisyphus.LoadDatabases(maildirs)
				if err != nil {
					log.WithFields(log.Fields{
						"err": err,
					}).Fatal("Cannot load databases")
				}
				defer sisyphus.CloseDatabases(dbs)

				// Learn at startup and regular intervals
				go func() {
					for {
						duration, err := time.ParseDuration(os.Getenv("SISYPHUS_DURATION"))
						if err != nil {
							log.Fatal("Cannot parse duration for learning intervals.")
						}

						backup(maildirs, dbs)
						learn(maildirs, dbs)
						time.Sleep(duration)
					}
				}()

				// Classify whenever a mail arrives in "new"
				watcher, err := fsnotify.NewWatcher()
				if err != nil {
					log.WithFields(log.Fields{
						"err": err,
					}).Fatal("Cannot setup directory watcher")
				}
				defer watcher.Close()

				done := make(chan bool)
				go func() {
					for {
						select {
						case event := <-watcher.Events:
							if event.Op&fsnotify.Create == fsnotify.Create {
								path := strings.Split(event.Name, "/new/")

								_, dryRun := os.LookupEnv("SISYPHUS_DRY_RUN")
								m := sisyphus.Mail{
									Key:    path[1],
									DryRun: dryRun,
								}

								err = m.Classify(dbs[sisyphus.Maildir(path[0])], sisyphus.Maildir(path[0]))
								if err != nil {
									log.WithFields(log.Fields{
										"err": err,
									}).Error("Classify mail")
								}

							}
						case err := <-watcher.Errors:
							log.WithFields(log.Fields{
								"err": err,
							}).Error("Problem with directory watcher")
						}
					}
				}()

				for _, val := range maildirs {
					err = watcher.Add(filepath.Join(string(val), "new"))
					if err != nil {
						log.WithFields(log.Fields{
							"err": err,
							"dir": filepath.Join(string(val), "new"),
						}).Error("Cannot watch directory")
					}
				}

				<-done
			},
		},
		{
			Name:    "stats",
			Aliases: []string{"i"},
			Usage:   "show statistics",
			Action: func(c *cli.Context) {

				maildirs := loadConfig()

				// Open all backup databases
				dbs, err := sisyphus.LoadBackupDatabases(maildirs)
				if err != nil {
					log.WithFields(log.Fields{
						"err": err,
					}).Fatal("Cannot load backup databases")
				}
				defer sisyphus.CloseDatabases(dbs)

				for _, db := range dbs {
					gTotal, jTotal, gWords, jWords := sisyphus.Info(db)
					log.WithFields(log.Fields{
						"good mails learned":   gTotal,
						"junk mails learned":   jTotal,
						"number of good words": gWords,
						"number of junk words": jWords,
					}).Info("Statistics")
				}
			},
		},
	}

	app.Run(os.Args)
}

// learn invokes the learning process for a slice of maildirs
func learn(maildirs []sisyphus.Maildir, dbs map[sisyphus.Maildir]*bolt.DB) {
	mails, err := sisyphus.LoadMails(maildirs)
	if err != nil {
		log.WithFields(log.Fields{
			"err": err,
		}).Fatal("Cannot load mails")
	}
	for _, d := range maildirs {
		db := dbs[d]
		m := mails[d]
		for _, val := range m {
			err := val.Learn(db, d)
			if err != nil {
				log.WithFields(log.Fields{
					"err":  err,
					"mail": val.Key,
				}).Warning("Cannot learn mail")
			}
		}
	}
	log.Info("All mails learned")

	return
}

// backup creates a backup copy of the existing database
func backup(maildirs []sisyphus.Maildir, dbs map[sisyphus.Maildir]*bolt.DB) {
	for _, d := range maildirs {
		db := dbs[d]

		backup, err := os.Create(filepath.Join(string(d), "sisyphus.db.backup"))

		if err != nil {
			log.WithFields(log.Fields{
				"err": err,
			}).Error("Backup creation")
		}
		defer backup.Close()

		w := bufio.NewWriter(backup)

		err = db.View(func(tx *bolt.Tx) error {
			_, err := tx.WriteTo(w)
			return err
		})
		if err != nil {
			log.WithFields(log.Fields{
				"err": err,
			}).Error("Backup creation")
		}

		w.Flush()
	}

	log.Info("All databases backed up successfully.")

	return
}

// loadConfig checks the validity of the environment variables and
// loads the maildirs
func loadConfig() []sisyphus.Maildir {

	dirsRaw, ok := os.LookupEnv("SISYPHUS_DIRS")
	if !ok {
		log.Fatal("Environment variable SISYPHUS_DIRS not set.")
	}
	dirsSplit := strings.Split(dirsRaw, ",")

	var maildirs []sisyphus.Maildir
	for i := 0; i < len(dirsSplit); i++ {
		maildirs = append(maildirs, sisyphus.Maildir(dirsSplit[i]))
	}

	// Create missing Maildirs
	err := sisyphus.LoadMaildirs(maildirs)
	if err != nil {
		log.WithFields(log.Fields{
			"err": err,
		}).Fatal("Cannot load maildirs")
	}

	// Check duration configuration and set it to default value if
	// not set
	_, ok = os.LookupEnv("SISYPHUS_DURATION")
	if !ok {
		log.Info("Environment variable SISYPHUS_DURATION not set. Setting default value to 24h.")
		os.Setenv("SISYPHUS_DURATION", "24h")
	}

	return maildirs

}
