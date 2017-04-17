package gogram

import (
	"regexp"
	"strings"
	"time"

	"github.com/syndtr/goleveldb/leveldb"
	"github.com/syndtr/goleveldb/leveldb/util"

	"gopkg.in/telegram-bot-api.v4"
)

type BotBackend struct {
	routes           []*route
	attachmentRoutes []*attachmentRoute
	mq               chan<- tgbotapi.Chattable
	bot              *tgbotapi.BotAPI
	db               *leveldb.DB
	closeChan        chan bool
}

type routable interface {
}

type route struct {
	routable
	prefix  string
	pattern *regexp.Regexp
	fn      routeFunc
}

type attachmentRoute struct {
	routable
	Type AttachmentType
	fn   routeFunc
}

type RouteContext struct {
	backend *BotBackend
	route   routable
	matches [][]string
	msg     *tgbotapi.Message
	target  int64
	text    string
}

type AttachmentType string

const (
	AttachmentPhoto   AttachmentType = "AttachmentPhoto"
	AttachmentVoice                  = "AttachmentVoice"
	AttachmentSticker                = "AttachmentSticker"
	AttachmentVideo                  = "AttachmentVideo"
)

type routeFunc func(ctx *RouteContext)

func newBotBackend(bot *tgbotapi.BotAPI, mq chan<- tgbotapi.Chattable) *BotBackend {
	backend := &BotBackend{
		mq:        mq,
		bot:       bot,
		closeChan: make(chan bool, 1),
	}

	return backend
}

func (b *BotBackend) open(dbPath string) {
	db, err := leveldb.OpenFile(dbPath, nil)
	if err != nil {
		panic(err)
	}

	b.db = db
}

// close closes all opened connection for lock-safety
func (b *BotBackend) close() {
	for i := 0; i < cap(b.closeChan); i++ {
		b.closeChan <- true
	}

	b.db.Close()
}

func (b *BotBackend) AddPeriodicRoute(t <-chan time.Time, target int64, fn routeFunc) {
	b.closeChan = make(chan bool, cap(b.closeChan)+1)
	go func(fn routeFunc) {
		for {
			select {
			case _, ok := <-t:
				if !ok {
					b.closeChan = make(chan bool, cap(b.closeChan)-1)
					return
				}

				ctx := &RouteContext{
					backend: b,
					target:  target,
				}
				fn(ctx)

			case <-b.closeChan:
				return
			}
		}
	}(fn)
}

func (b *BotBackend) AddRoute(prefix, pattern string, fn routeFunc) {
	if !strings.HasSuffix(pattern, "$") {
		pattern += "$"
	}
	r := &route{
		prefix:  prefix,
		pattern: regexp.MustCompile(pattern),
		fn:      fn,
	}

	b.routes = append(b.routes, r)
}

func (b *BotBackend) AddAttachmentRoute(attachType AttachmentType, fn routeFunc) {
	r := &attachmentRoute{
		Type: attachType,
		fn:   fn,
	}

	b.attachmentRoutes = append(b.attachmentRoutes, r)
}

func (b *BotBackend) route(msg *tgbotapi.Message) {
	if msg.Text == "" {
		var attachType AttachmentType
		if msg.Photo != nil {
			attachType = AttachmentPhoto
		}

		for _, route := range b.attachmentRoutes {
			if route.Type != attachType {
				continue
			}

			ctx := &RouteContext{
				backend: b,
				route:   route,
				msg:     msg,
				text:    msg.Caption,
				target:  msg.Chat.ID,
			}

			go route.fn(ctx)
		}
	}

	for _, route := range b.routes {
		var text string
		if route.prefix != "" && !strings.HasPrefix(msg.Text, route.prefix) {
			continue
		} else {
			text = msg.Text[len(route.prefix):]
		}

		matches := route.pattern.FindAllStringSubmatch(text, -1)
		if len(matches) == 0 {
			continue
		}

		ctx := &RouteContext{
			backend: b,
			route:   route,
			matches: matches,
			msg:     msg,
			text:    text,
			target:  msg.Chat.ID,
		}

		route.fn(ctx)
		return
	}
}

// NewReply returns an instance of tgbotapi.MessageConfig, which will be used for .Send method
func (ctx *RouteContext) NewReply(ref bool) tgbotapi.MessageConfig {
	var msg tgbotapi.MessageConfig
	if ctx.msg != nil {
		msg = tgbotapi.NewMessage(ctx.msg.Chat.ID, "")
		if ref {
			msg.ReplyToMessageID = ctx.msg.MessageID
		}
	} else if ctx.target != 0 {
		msg = tgbotapi.NewMessage(ctx.target, "")
	}

	return msg
}

func (ctx *RouteContext) NewPhotoShareReply(photoID string) tgbotapi.PhotoConfig {
	msg := tgbotapi.NewPhotoShare(ctx.target, photoID)
	return msg
}

func (ctx *RouteContext) NewPhotoUploadReply(file interface{}) tgbotapi.PhotoConfig {
	msg := tgbotapi.NewPhotoUpload(ctx.target, file)
	return msg
}

func (ctx *RouteContext) Photo() []tgbotapi.PhotoSize {
	return *ctx.msg.Photo
}

func (ctx *RouteContext) Matches() [][]string {
	return ctx.matches
}

func (ctx *RouteContext) Text() string {
	return ctx.text
}

func (ctx *RouteContext) ChatID() int64 {
	return ctx.target
}

func (ctx *RouteContext) GetUser() *tgbotapi.User {
	return ctx.msg.From
}

func (ctx *RouteContext) GetUserByID(id int) *tgbotapi.User {
	config := tgbotapi.ChatConfigWithUser{
		ChatID: ctx.target,
		UserID: id,
	}
	cm, err := ctx.backend.bot.GetChatMember(config)
	if err != nil {
		return nil
	}

	return cm.User
}

// Send queues a message
func (ctx *RouteContext) Send(msg tgbotapi.Chattable) {
	ctx.backend.mq <- msg
}

func (ctx *RouteContext) Get(key string) []byte {
	var value []byte
	if has, _ := ctx.backend.db.Has([]byte(key), nil); !has {
		return nil
	}

	value, _ = ctx.backend.db.Get([]byte(key), nil)
	if len(value) == 0 {
		return nil
	}

	return value
}

func (ctx *RouteContext) GetPrefix(prefix string) (items map[string][]byte) {
	items = map[string][]byte{}
	iter := ctx.backend.db.NewIterator(util.BytesPrefix([]byte(prefix)), nil)
	for iter.Next() {
		key := string(iter.Key())
		refValue := iter.Value()
		value := make([]byte, len(refValue))
		copy(value, iter.Value())
		items[key] = value
	}
	iter.Release()
	err := iter.Error()
	if err != nil {
		return nil
	}

	return items
}

func (ctx *RouteContext) Set(key string, value []byte) bool {
	err := ctx.backend.db.Put([]byte(key), value, nil)
	return err == nil
}

func (ctx *RouteContext) Del(key string) bool {
	err := ctx.backend.db.Delete([]byte(key), nil)
	return err == nil
}
