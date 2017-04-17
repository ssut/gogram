package gogram

import (
	"flag"

	"gopkg.in/telegram-bot-api.v4"
)

type Gogram struct {
	token   string
	bot     *tgbotapi.BotAPI
	Backend *BotBackend
	mq      chan tgbotapi.Chattable
}

func NewGogram(apiToken string) *Gogram {
	bot, err := tgbotapi.NewBotAPI(apiToken)
	if err != nil {
		log.Panic(err)
	}

	gogram := &Gogram{
		bot:   bot,
		token: apiToken,
		mq:    make(chan tgbotapi.Chattable, 10),
	}

	backend := newBotBackend(bot, gogram.mq)
	gogram.Backend = backend

	return gogram
}

func (g *Gogram) messageWorker() {
	for {
		m := <-g.mq

		g.bot.Send(m)
	}
}

func (g *Gogram) Start() {
	dbPath := flag.String("db", "database", "leveldb path")
	workerCount := flag.Int("threads", 2, "num of threads used to send messages")
	timeout := flag.Int("timeout", 60, "polling timeout for telegram web client")
	debug := flag.Bool("debug", false, "enable debug mode for gogram")
	botDebug := flag.Bool("debug-bot", false, "enable debug mode for telegram web client")
	flag.Parse()

	initLogger(*debug)
	g.bot.Debug = *botDebug
	log.Printf("Authorized on account %s", g.bot.Self.UserName)

	u := tgbotapi.NewUpdate(0)
	u.Timeout = *timeout

	updates, _ := g.bot.GetUpdatesChan(u)
	for i := 0; i < *workerCount; i++ {
		go g.messageWorker()
	}

	g.Backend.open(*dbPath)
	defer g.Backend.close()
	for update := range updates {
		if update.Message == nil {
			continue
		}

		log.Printf("[%s] %s", update.Message.From.UserName, update.Message.Text)
		go g.Backend.route(update.Message)
	}
}
