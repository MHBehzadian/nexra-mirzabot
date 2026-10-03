// Command nexrabot is the Go version of the Nexra Mirza bot.
//
//	nexrabot serve        -c /etc/nexrabot/bot7.env
//	nexrabot migrate-php  --php-dir /var/www/html/botmirzapanel7 --out /etc/nexrabot/bot7.env --listen 127.0.0.1:8107
//	nexrabot import-sql   -c /etc/nexrabot/bot7.env --file backup.sql [--force]
//	nexrabot schema       -c /etc/nexrabot/bot7.env
//	nexrabot set-webhook  -c /etc/nexrabot/bot7.env
//	nexrabot keys         -c /etc/nexrabot/bot7.env
//	nexrabot autopay-test -c ... parse | status | send <amount> | send-raw <text>
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/MHBehzadian/nexra-mirzabot/internal/api"
	"github.com/MHBehzadian/nexra-mirzabot/internal/bot"
	"github.com/MHBehzadian/nexra-mirzabot/internal/config"
	"github.com/MHBehzadian/nexra-mirzabot/internal/db"
	"github.com/MHBehzadian/nexra-mirzabot/internal/migrate"
	"github.com/MHBehzadian/nexra-mirzabot/internal/php"
	"github.com/MHBehzadian/nexra-mirzabot/internal/tg"
	"github.com/MHBehzadian/nexra-mirzabot/internal/web"
)

func usage() {
	fmt.Fprintln(os.Stderr, `usage: nexrabot <command> [flags]

commands:
  serve         run the bot (webhook, crons, management API)
  migrate-php   create a config from an existing PHP bot install
  import-sql    load a mysqldump backup into the configured database
  schema        create/upgrade the database tables (safe to repeat)
  set-webhook   point the Telegram webhook at this bot
  keys          print the management API keys for Nexra Panel
  autopay-test  check the bank SMS parser / simulate a deposit
  version       print the version`)
	os.Exit(2)
}

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	cmd, args := os.Args[1], os.Args[2:]
	switch cmd {
	case "serve":
		cmdServe(args)
	case "migrate-php":
		cmdMigrate(args)
	case "import-sql":
		cmdImport(args)
	case "schema":
		cmdSchema(args)
	case "set-webhook":
		cmdWebhook(args)
	case "keys":
		cmdKeys(args)
	case "autopay-test":
		cmdAutopayTest(args)
	case "version", "--version", "-v":
		fmt.Println(bot.Version)
	default:
		usage()
	}
}

func die(format string, a ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", a...)
	os.Exit(1)
}

func loadConfig(fs *flag.FlagSet, args []string) *config.Config {
	path := fs.String("c", os.Getenv("NEXRABOT_CONFIG"), "config file")
	_ = fs.Parse(args)
	cfg, err := config.Load(*path)
	if err != nil {
		die("%v", err)
	}
	if err := cfg.Validate(); err != nil {
		die("%v", err)
	}
	return cfg
}

func openDB(cfg *config.Config) *db.DB {
	var d *db.DB
	var err error
	for i := 0; i < 10; i++ {
		d, err = db.Open(cfg.DSN())
		if err == nil {
			return d
		}
		time.Sleep(2 * time.Second)
	}
	die("database: %v", err)
	return nil
}

func cmdServe(args []string) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	cfg := loadConfig(fs, args)
	logger := log.New(os.Stdout, "", log.LstdFlags)
	db.Logger = logger.Printf
	d := openDB(cfg)
	if err := d.EnsureSchema(cfg.AdminID, func(s string) { logger.Println("schema:", s) }); err != nil {
		die("schema: %v", err)
	}
	if cfg.APIOwnerKey == "" || cfg.APIManagerKey == "" {
		logger.Println("warning: API_OWNER_KEY / API_MANAGER_KEY not set; the management API is disabled")
	}
	t := tg.New(cfg.BotToken, cfg.TelegramAPI, cfg.TelegramProxy)
	t.Log = logger
	b := bot.New(cfg, d, t, logger)
	b.Start()

	srv := &web.Server{B: b}
	if cfg.APIOwnerKey != "" || cfg.APIManagerKey != "" {
		srv.API = api.New(b)
	}
	stop := make(chan struct{})
	if !cfg.DisableCrons {
		b.RunCrons(stop)
	}
	hs := srv.Run(cfg.Listen)
	go func() {
		logger.Printf("nexrabot %s listening on %s for @%s", bot.Version, cfg.Listen, cfg.BotUsername)
		if err := hs.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			die("http: %v", err)
		}
	}()
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	logger.Println("shutting down")
	close(stop)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	_ = hs.Shutdown(ctx)
	b.Stop()
}

func cmdMigrate(args []string) {
	fs := flag.NewFlagSet("migrate-php", flag.ExitOnError)
	dir := fs.String("php-dir", "", "directory of the PHP bot (holding config.php)")
	out := fs.String("out", "", "config file to write (e.g. /etc/nexrabot/bot7.env)")
	listen := fs.String("listen", "127.0.0.1:8080", "address the Go bot will listen on")
	dataDir := fs.String("data-dir", "/var/lib/nexrabot", "data directory")
	cleanCron := fs.Bool("clean-crontab", true, "remove this bot's /cron/*.php crontab lines (the Go bot runs them itself)")
	_ = fs.Parse(args)
	if *dir == "" || *out == "" {
		die("--php-dir and --out are required")
	}
	cfg, err := migrate.ReadPHPConfig(strings.TrimRight(*dir, "/") + "/config.php")
	if err != nil {
		die("%v", err)
	}
	cfg.Listen = *listen
	cfg.DataDir = *dataDir
	cfg.LegacyPHPDir = *dir
	if old, err := config.Load(*out); err == nil && old.APIOwnerKey != "" {
		// re-running keeps the keys Nexra Panel already uses
		cfg.APIOwnerKey, cfg.APIManagerKey, cfg.WebhookSecret = old.APIOwnerKey, old.APIManagerKey, old.WebhookSecret
	}
	if cfg.APIOwnerKey == "" {
		cfg.APIOwnerKey = config.RandomKey(24)
		cfg.APIManagerKey = config.RandomKey(24)
		cfg.WebhookSecret = config.RandomKey(16)
	}
	full, _ := config.Load("")
	cfg.DBSocket = full.DBSocket
	if err := cfg.Write(*out); err != nil {
		die("writing %s: %v", *out, err)
	}
	fmt.Println("config written:", *out)

	d, err := db.Open(cfg.DSN())
	if err != nil {
		die("database (check the credentials in %s): %v", *out, err)
	}
	if err := d.EnsureSchema(cfg.AdminID, func(s string) { fmt.Println("  schema:", s) }); err != nil {
		die("schema: %v", err)
	}
	crons, _ := migrate.CronState(cfg.Domain)
	for name, on := range crons {
		v := "0"
		if on {
			v = "1"
		}
		if _, exists := d.KVOk("cron_" + name); !exists {
			d.SetKV("cron_"+name, v)
		}
	}
	fmt.Printf("cron switches carried over: %v\n", crons)
	if *cleanCron {
		if old, err := migrate.RemoveCronLines(cfg.Domain); err != nil {
			fmt.Println("warning:", err)
		} else if old != "" {
			_ = os.WriteFile(strings.TrimSuffix(*out, ".env")+".crontab.bak", []byte(old), 0600)
			fmt.Println("old /cron/*.php crontab lines removed (backup next to the config)")
		}
	}
	fmt.Println("done. Users, services, products, payments and settings stay exactly where they were.")
}

func cmdImport(args []string) {
	fs := flag.NewFlagSet("import-sql", flag.ExitOnError)
	file := fs.String("file", "", "mysqldump file (.sql or .sql.gz)")
	force := fs.Bool("force", false, "overwrite a database that already has users")
	cfg := loadConfig(fs, args)
	if *file == "" {
		die("--file is required")
	}
	d := openDB(cfg)
	err := migrate.ImportSQL(d, *file, *force, func(n int) { fmt.Printf("\r  %d statements", n) })
	fmt.Println()
	if err != nil {
		die("import failed: %v", err)
	}
	if err := d.EnsureSchema(cfg.AdminID, func(s string) { fmt.Println("  schema:", s) }); err != nil {
		die("schema: %v", err)
	}
	fmt.Println("imported and upgraded.")
}

func cmdSchema(args []string) {
	fs := flag.NewFlagSet("schema", flag.ExitOnError)
	cfg := loadConfig(fs, args)
	d := openDB(cfg)
	if err := d.EnsureSchema(cfg.AdminID, func(s string) { fmt.Println(s) }); err != nil {
		die("%v", err)
	}
	fmt.Println("schema is up to date")
}

func cmdWebhook(args []string) {
	fs := flag.NewFlagSet("set-webhook", flag.ExitOnError)
	cfg := loadConfig(fs, args)
	t := tg.New(cfg.BotToken, cfg.TelegramAPI, cfg.TelegramProxy)
	r := t.SetWebhook("https://"+cfg.Domain+"/index.php", cfg.WebhookSecret)
	if !r.OK {
		die("setWebhook failed: %s", r.Description)
	}
	fmt.Println("webhook set to https://" + cfg.Domain + "/index.php")
}

func cmdKeys(args []string) {
	fs := flag.NewFlagSet("keys", flag.ExitOnError)
	cfg := loadConfig(fs, args)
	fmt.Println("bot:          @" + cfg.BotUsername)
	fmt.Println("api url:      https://" + cfg.Domain + "/api/v1")
	fmt.Println("owner key:    " + cfg.APIOwnerKey)
	fmt.Println("manager key:  " + cfg.APIManagerKey)
}

func cmdAutopayTest(args []string) {
	fs := flag.NewFlagSet("autopay-test", flag.ExitOnError)
	path := fs.String("c", os.Getenv("NEXRABOT_CONFIG"), "config file")
	_ = fs.Parse(args)
	rest := fs.Args()
	sub := "status"
	if len(rest) > 0 {
		sub = rest[0]
	}
	samples := []string{
		"بانک ملت\nواریز:۱۲۵,۳۰۰ ریال\n۱۴۰۵/۰۷/۱۱-۱۲:۳۳\nمانده:۵,۴۳۲,۱۰۰ ریال",
		"ملی\n*1234\nمبلغ واریز 1253000 ریال\nمانده 9870000",
		"بانک سامان\nبرداشت 500,000 ریال\nمانده 1,000,000 ریال",
		"انتقال از 6219-****-1234\nبستانکار 1,253,000 ریال\nساعت 14:02",
	}
	if sub == "parse" {
		for i, s := range samples {
			p := bot.ParseSMS(s)
			fmt.Printf("--- sample %d\n   direction : %s\n   amount    : %s toman\n   candidates: %v\n   card      : %s\n", i+1, p.Direction, php.NumberFormat(float64(p.Amount), 0), p.Candidates, p.Card)
		}
		return
	}
	cfg, err := config.Load(*path)
	if err != nil {
		die("%v", err)
	}
	if err := cfg.Validate(); err != nil {
		die("%v", err)
	}
	d := openDB(cfg)
	t := tg.New(cfg.BotToken, cfg.TelegramAPI, cfg.TelegramProxy)
	b := bot.New(cfg, d, t, log.New(os.Stdout, "", 0))
	switch sub {
	case "status":
		s := b.AutopaySettings()
		fmt.Println("status    :", s.S("status"))
		fmt.Println("last seen :", s.S("last_seen"))
		fmt.Println("device    :", s.S("device_info"))
		fmt.Println(b.AutopayOpenOrders())
		fmt.Println(b.AutopayRecentSMS())
	case "send":
		if len(rest) < 2 || php.Intval(rest[1]) <= 0 {
			die("usage: nexrabot autopay-test -c FILE send <amount in toman>")
		}
		amount := php.Intval(rest[1])
		body := "بانک تست\nواریز " + php.NumberFormat(float64(amount*10), 0) + " ریال\nمانده 1,000,000 ریال"
		fmt.Println(b.AutopayHandleSMS(body, "TEST", php.DateNow("Y-m-d H:i:s")))
	case "send-raw":
		if len(rest) < 2 {
			die("usage: nexrabot autopay-test -c FILE send-raw \"<sms text>\"")
		}
		fmt.Println(b.AutopayHandleSMS(rest[1], "TEST", php.DateNow("Y-m-d H:i:s")))
	default:
		die("commands: parse | status | send <amount> | send-raw <text>")
	}
}
