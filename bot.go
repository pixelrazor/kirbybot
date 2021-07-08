package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"

	"github.com/boltdb/bolt"
	"github.com/bwmarrin/discordgo"

	"github.com/dghubble/go-twitter/twitter"
	"github.com/dghubble/oauth1"
)

var (
	repo ConfigRepository
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

	if *mem {
		log.Println("Using memory")
		repo = NewMapRepo()
	} else {
		db, err := bolt.Open(filepath.Join(*dbDir, "kirb.db"), 0600, nil)
		if err != nil {
			log.Fatalln("Failed to open DB:", err)
		}
		defer db.Close()
	}

	dg, err := discordgo.New("Bot " + env[discordTokenKey])
	if err != nil {
		log.Fatalln("Failed to create dg session:", err)
	}

	dg.AddHandler(onReady)
	dg.AddHandler(onMessage)

	if err := dg.Open(); err != nil {
		log.Fatalln("Failed to start dg session:", err)
	}
	defer dg.Close()

	go listenToKirby(dg, env)

	// Wait for SIGINT and SIGTERM (HIT CTRL-C)
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
	log.Println("Quitting from signal:", <-ch)
}

func listenToKirby(s *discordgo.Session, env map[string]string) {
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
					doKirbPost(m, s)
				case error:
					log.Println("Tweet stream error:", err)
				}
			}
			log.Println("Restarting tweet stream")
		}()
	}
}

func doKirbPost(tweet *twitter.Tweet, s *discordgo.Session) {
	if tweet.Retweeted || tweet.RetweetedStatus != nil || tweet.InReplyToUserID != 0 {
		return
	}
	msg := tweet.Text
	if len(tweet.FullText) > len(msg) {
		msg = tweet.FullText
	}

	// Get channels to post tweets to
	channels := repo.GetKirbChannels()

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
					"so he can see what's up. (This may be useful to him: \"%v\")", channel, sendError))
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

func onReady(s *discordgo.Session, r *discordgo.Ready) {
	log.Println("Kirby reporting for duty!")
}

func onMessage(s *discordgo.Session, m *discordgo.MessageCreate) {
	if m.Author.Bot {
		return
	}
	fields := strings.Fields(m.Content)
	if len(fields) == 0 {
		return
	}
	// TODO: slash commands when discordgo tags a new release and has the permissions API
	if fields[0] == "!kb" {
		if len(fields) == 1 {
			fields = append(fields, "help")
		}
		switch fields[1] {
		case "set-kirb-post":
			if !isAdmin(s, m.GuildID, m.Author.ID) {
				s.ChannelMessageSend(m.ChannelID, "You don't have permission for that!")
				break
			}
			// TODO: use the channel given, defaulting to current channel if no channel is supplied
			repo.SetKirbChannel(m.GuildID, m.ChannelID)
			s.ChannelMessageSend(m.ChannelID, "Let the kirb posting commence!")
		case "remove-kirb-post":
			if !isAdmin(s, m.GuildID, m.Author.ID) {
				s.ChannelMessageSend(m.ChannelID, "You don't have permission for that!")
				break
			}
			repo.RemoveKirbChannel(m.GuildID)
			s.ChannelMessageSend(m.ChannelID, "No more kirb posting :c")
		case "check-kirb-post":
			if !isAdmin(s, m.GuildID, m.Author.ID) {
				s.ChannelMessageSend(m.ChannelID, "You don't have permission for that!")
				break
			}
			ch, ok := repo.GetKirbChannels()[m.GuildID]
			if !ok {
				s.ChannelMessageSend(m.ChannelID, "No kirb posting on this server :c")
				return
			}
			s.ChannelMessageSend(m.ChannelID, fmt.Sprintf("Kirb posting set to happen in <#%v>", ch))
		case "help":
			err := postHelp(s, m.ChannelID)
			if err != nil {
				log.Println("Failed to post help menu:", err)
			}
		default:
			s.ChannelMessageSend(m.ChannelID, "I dunno what you're tellin me to do")
		}
	}
}

func postHelp(s *discordgo.Session, ch string) error {
	_, err := s.ChannelMessageSendEmbed(ch, &discordgo.MessageEmbed{
		Title:       "Help",
		Description: "The following are all commands I respond to (**bold** commands require admin perms)",
		Color:       embedColor,
		Fields: []*discordgo.MessageEmbedField{
			{
				Inline: false,
				Name:   "**set-kirb-post**",
				Value:  "Sets the current channel as the kirb posting channel (overrides previously set channel)",
			}, {
				Inline: false,
				Name:   "**remove-kirb-post**",
				Value:  "Removes kirb posting from the server (can be run from ANY channel)",
			}, {
				Inline: false,
				Name:   "**check-kirb-post**",
				Value:  "See whether or not kirb-posting is enabled, and what channel it is set to",
			}, {
				Inline: false,
				Name:   "help",
				Value:  "See this menu again",
			},
		},
	})
	return err
}

func isAdmin(s *discordgo.Session, guild, member string) bool {
	m, err := s.GuildMember(guild, member)
	if err != nil {
		log.Println("Failed to get guild", guild, "member", member, "-", err)
		return false
	}
	roles, err := s.GuildRoles(guild)
	if err != nil {
		log.Println("Failed to get guild", guild, "roles:", err)
		return false
	}
	for _, role := range roles {
		for _, id := range m.Roles {
			if id == role.ID {
				if role.Permissions&discordgo.PermissionAdministrator != 0 {
					return true
				}
				break
			}
		}
	}
	return false
}
