package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/boltdb/bolt"
	"github.com/bwmarrin/discordgo"

	"github.com/dghubble/go-twitter/twitter"
	"github.com/dghubble/oauth1"
)

const (
	discordTokenKey          = "DISCORD_TOKEN"
	twitterConsumerKeyKey    = "TWITTER_CONSUMER_KEY"
	twitterConsumerSecretKey = "TWITTER_CONSUMER_SECRET"
	twitterAccessTokenKey    = "TWITTER_ACCESS_TOKEN"
	twitterAccessSecretKey   = "TWITTER_ACCESS_SECRET"
)

const embedColor = 0xffa6c9

func main() {
	env := map[string]string{
		discordTokenKey:          "",
		twitterConsumerKeyKey:    "",
		twitterConsumerSecretKey: "",
		twitterAccessTokenKey:    "",
		twitterAccessSecretKey:   "",
	}
	for k := range env {
		val, ok := os.LookupEnv(k)
		if !ok {
			log.Fatalln("Missing " + k + " in env")
		}
		env[k] = val
	}

	dbDir := flag.String("dbdir", "/data", "Directory where database is stored (if not running in memory mode)")
	mem := flag.Bool("mem", false, "Use an in-memory data store instead of bolt DB")
	flag.Parse()

	var repo ConfigRepository
	if *mem {
		log.Println("Using memory")
		repo = NewMapRepo()
	} else {
		db, err := bolt.Open(filepath.Join(*dbDir, "kirb.db"), 0600, nil)
		if err != nil {
			log.Fatalln("Failed to open DB:", err)
		}
		defer db.Close()
		repo = NewBoltRepo(db)
	}

	key := "Bot " + env[discordTokenKey]
	dg, err := discordgo.New(key)
	if err != nil {
		log.Fatalln("Failed to create dg session:", err)
	}

	bot := KirbyBot{
		s:    dg,
		repo: repo,
	}

	bot.handlers()

	if err := bot.run(); err != nil {
		log.Fatalln("Failed to start bot:", err)
	}

	go bot.listenToKirby(dg, env)

	// Wait for SIGINT and SIGTERM (HIT CTRL-C)
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
	log.Println("Quitting from signal:", <-ch)
}

func (kb *KirbyBot) listenToKirby(s *discordgo.Session, env map[string]string) {
	config := oauth1.NewConfig(env[twitterConsumerKeyKey], env[twitterConsumerSecretKey])
	token := oauth1.NewToken(env[twitterAccessTokenKey], env[twitterAccessSecretKey])
	// OAuth1 http.Client will automatically authorize Requests
	httpClient := config.Client(oauth1.NoContext, token)

	// Twitter Client
	client := twitter.NewClient(httpClient)

	filterParams := &twitter.StreamFilterParams{
		Follow:        []string{"826639173557837824"}, // Kirby bot twitter account ID
		StallWarnings: twitter.Bool(true),
		FilterLevel:   "none",
	}
	log.Println("Listening to kirby...")
	for {
		func() {
			stream, err := client.Streams.Filter(filterParams)
			if err != nil {
				log.Fatalln(err)
			}
			defer stream.Stop()
			for message := range stream.Messages {
				switch m := message.(type) {
				case *twitter.Tweet:
					kb.doKirbPost(m, s)
				case error:
					log.Println("Tweet stream error:", err)
				}
			}
		}()
		time.Sleep(30 * time.Second)
		log.Println("Restarting tweet stream")
	}
}

func (kb *KirbyBot) doKirbPost(tweet *twitter.Tweet, s *discordgo.Session) {
	if tweet.Retweeted || tweet.RetweetedStatus != nil || tweet.InReplyToUserID != 0 {
		return
	}
	msg := tweet.Text
	if len(tweet.FullText) > len(msg) {
		msg = tweet.FullText
	}

	// Get channels to post tweets to
	channels, err := kb.repo.GetKirbChannels()
	if err != nil {
		log.Println("Failed to get kirb posting channels:", err)
		return
	}

	wg := sync.WaitGroup{}
	for guild, channel := range channels {
		wg.Add(1)
		guild, channel := guild, channel
		go func() {
			defer wg.Done()
			_, err := s.ChannelMessageSend(channel, msg)
			if err != nil {
				sendError := err
				log.Println("Failed to send kirb post to guild:", guild, "channel:", channel)
				g, err := s.Guild(guild)
				if err != nil {
					log.Println("Failed to get guild:", guild)
					return
				}
				ch, err := s.UserChannelCreate(g.OwnerID)
				if err != nil {
					log.Println("Failed to create DM channel with server owner. guild:", guild, "owner:", g.OwnerID, "-", err)
					return
				}
				_, err = s.ChannelMessageSend(ch.ID, fmt.Sprintf("Hey there! It looks like I failed to kirb post in <#%v>. "+
					"Please make sure I have permission to post there. If I do have permission, maybe message my owner "+
					"so they can see what's up. (This may be useful to them: \"%v\"). Try the about command to get contact info!", channel, sendError))
				if err != nil {
					log.Println("Failed to message server owner. guild:", guild, "owner:", g.OwnerID, "-", err)
					return
				}
			}
		}()
	}
	// Block until everything is done. This is to ensure that the caller can ensure tweets are sent in order
	wg.Wait()
}

type KirbyBot struct {
	s    *discordgo.Session
	repo ConfigRepository
}

type KirbyBotInteraction struct {
	command *discordgo.ApplicationCommand
	handler func(s *discordgo.Session, i *discordgo.InteractionCreate)
}

func commandPermissions(permissions ...int) *int64 {
	var permission int64
	for _, p := range permissions {
		permission |= int64(p)
	}
	return &permission
}

func (kb *KirbyBot) registeredInteractions() []KirbyBotInteraction {
	falseValue := false
	return []KirbyBotInteraction{
		{
			command: &discordgo.ApplicationCommand{
				Name:                     "channel",
				Description:              "Manage the kirb-posting channel",
				DefaultMemberPermissions: commandPermissions(discordgo.PermissionAdministrator),
				DMPermission:             &falseValue,
				Options: []*discordgo.ApplicationCommandOption{
					{
						Type:        discordgo.ApplicationCommandOptionSubCommand,
						Name:        "set",
						Description: "Sets the current channel as the kirb-posting channel (overrides previously set channel)",
						Options: []*discordgo.ApplicationCommandOption{
							{
								Type:        discordgo.ApplicationCommandOptionChannel,
								Name:        "channel",
								Description: "Channel to kirb-post to (defaults to current channel)",
							},
						},
					},
					{
						Type:        discordgo.ApplicationCommandOptionSubCommand,
						Name:        "check",
						Description: "See whether or not kirb-posting is enabled, and what channel it is set to",
					},
					{
						Type:        discordgo.ApplicationCommandOptionSubCommand,
						Name:        "remove",
						Description: "Removes kirb-posting from the server",
					},
				},
			},
			handler: kb.channelHandler,
		},
		{
			command: &discordgo.ApplicationCommand{
				Name:        "about",
				Description: "About Kirby Bot",
			},
			handler: kb.aboutHandler,
		},
	}
}

func (kb *KirbyBot) handlers() {
	interactionHandlers := make(map[string]func(s *discordgo.Session, i *discordgo.InteractionCreate))
	for _, interaction := range kb.registeredInteractions() {
		interactionHandlers[interaction.command.Name] = interaction.handler

	}
	kb.s.AddHandler(func(s *discordgo.Session, i *discordgo.InteractionCreate) {
		if handle, ok := interactionHandlers[i.ApplicationCommandData().Name]; ok {
			handle(s, i)
		}
	})
	kb.s.AddHandler(kb.handleOnReady())
}

func (kb *KirbyBot) run() error {
	if err := kb.s.Open(); err != nil {
		return fmt.Errorf("failed to start session: %w", err)
	}

	for _, v := range kb.registeredInteractions() {
		_, err := kb.s.ApplicationCommandCreate(kb.s.State.User.ID, "", v.command)
		if err != nil {
			return fmt.Errorf("cannot create '%v' command: %w", v.command.Name, err)
		}
	}

	return nil
}

func (kb *KirbyBot) handleOnReady() interface{} {
	return func(s *discordgo.Session, r *discordgo.Ready) {
		log.Println("Kirby reporting for duty!")
	}
}

func (kb *KirbyBot) aboutHandler(s *discordgo.Session, i *discordgo.InteractionCreate) {
	s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Embeds: []*discordgo.MessageEmbed{
				{
					Title: "About Kirby Bot",
					Description: fmt.Sprintf("Kirby bot provides kirby posting to servers far and wide! "+
						"It currently brings joy and kirby posting to %v servers. Would you like "+
						"Kirby Bot in one of your servers? [Click here!](%v) Kirby Bot is no-nonsense "+
						"and [open source](https://github.com/pixelrazor/kirbybot). For any questions, "+
						"feedback, or ideas, feel free to reach out to my creator through the information "+
						"available in the github link",
						len(s.State.Guilds),
						"https://discord.com/oauth2/authorize?client_id=723217306557218827&scope=bot&permissions=388160"),
					Color: embedColor,
				},
			},
		},
	})
}

func (kb *KirbyBot) channelHandler(s *discordgo.Session, i *discordgo.InteractionCreate) {
	options := i.ApplicationCommandData().Options
	mesg := ""
	var err error
	switch options[0].Name {
	case "set":
		channel := i.ChannelID
		if len(options[0].Options) > 0 {
			channel = options[0].Options[0].ChannelValue(s).ID
		}

		err = kb.repo.SetKirbChannel(i.GuildID, channel)
		if err != nil {
			break
		}
		mesg = fmt.Sprintf("Let the kirb posting commence in <#%v>!", channel)
	case "check":
		var channels map[string]string
		channels, err = kb.repo.GetKirbChannels()
		if err != nil {
			break
		}
		ch, ok := channels[i.GuildID]
		if !ok {
			mesg = "No kirb posting on this server :c"
			break
		}
		mesg = fmt.Sprintf("Kirb posting set to happen in <#%v>", ch)
	case "remove":
		kb.repo.RemoveKirbChannel(i.GuildID)
		mesg = "No more kirb posting :c"
	}
	if err != nil {
		log.Printf("Failed channel %v: %v\n", options[0].Name, err)
		mesg = fmt.Sprintf("Failed: %v", err)
	}
	s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: mesg,
		},
	})
}
