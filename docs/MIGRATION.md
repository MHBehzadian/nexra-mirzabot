# انتقال ربات PHP به Go

خلاصه: ربات Go **همان دیتابیس** را می‌خواند و می‌نویسد و **همان آدرس‌ها** را جواب می‌دهد (`/index.php` برای وبهوک، `/autopay.php` برای
اپ تأیید خودکار، `/payment/...` برای درگاه‌ها). پس انتقال فقط یعنی nginx به‌جای php-fpm درخواست‌ها را به ربات Go بدهد. داده‌ای منتقل
نمی‌شود، وبهوک عوض نمی‌شود و کاربران چیزی حس نمی‌کنند.

## انتقال خودکار (پیشنهادی)

```bash
curl -sLo install.sh https://raw.githubusercontent.com/MHBehzadian/nexra-mirzabot/main/install.sh
N=7 bash install.sh migrate
# پوشه‌ی دیگری؟  N=7 PHP_DIR=/var/www/html/mybot bash install.sh migrate
```

مراحل، به ترتیب:

1. **بررسی‌ها، بدون هیچ تغییری:** `config.php` پیدا می‌شود؛ سایت nginx که این پوشه را با php-fpm سرو می‌کند پیدا می‌شود و تغییرش روی یک کپی
   امتحان می‌شود. اگر چیدمان nginx ناآشنا باشد، همین‌جا متوقف می‌شود و چیزی دست نمی‌خورد.
2. فایل اجرایی در `/usr/local/bin/nexrabot` و سرویس `nexrabot@.service` نصب می‌شوند (کاربر سیستمی `nexrabot`).
3. تنظیمات از `config.php` خوانده می‌شود و در `/etc/nexrabot/bot7.env` نوشته می‌شود (توکن، دیتابیس، دامنه، کد مخفی، به‌علاوه‌ی کلیدهای
   API و توکن مخفی وبهوک که تازه ساخته می‌شوند).
4. **بکاپ کامل دیتابیس** در `/root/nexrabot-backups/bot7-<تاریخ>.sql.gz`، قبل از هر تغییری در دیتابیس.
5. اتصال به دیتابیس و توکن ربات آزمایش می‌شود.
6. جدول‌ها به‌روز می‌شوند: فقط جدول/ستون‌های جاافتاده اضافه می‌شوند (همان کار `table.php`) به‌علاوه‌ی دو جدول کوچک `nexra_kv` و
   `nexra_broadcast`. چیزی پاک یا عوض نمی‌شود و ربات PHP همچنان کار می‌کند. وضعیت روشن/خاموش کرون‌ها از crontab خوانده و منتقل می‌شود.
7. ربات Go یک بار آزمایشی (با کرون‌های خاموش) بالا می‌آید. اگر بالا نیاید، کار همین‌جا تمام می‌شود و ربات PHP دست‌نخورده است.
8. **جابه‌جایی** (از این‌جا هر خطایی، همه‌چیز را خودکار به PHP برمی‌گرداند):
   - از سایت nginx و crontab نسخه‌ی پشتیبان در `/etc/nexrabot/` گرفته می‌شود؛
   - خط‌های `https://دامنه/cron/*.php` از crontab برداشته می‌شوند (بقیه‌ی خط‌ها، مثل بکاپ ساعتی، می‌مانند)؛
   - سرویس `nexrabot@7` روشن می‌شود؛
   - در سایت nginx، بلوک `location /` با `include /etc/nginx/snippets/nexrabot7.conf` (proxy به ‎127.0.0.1:18007) جایگزین و بلوک
     `location ~ \.php$` برداشته می‌شود. `nginx -t` و reload انجام می‌شود (بدون قطعی؛ درخواست‌های در جریان PHP تمام می‌شوند)؛
   - `https://دامنه/healthz` باید جواب ربات Go را بدهد.
9. وبهوک با توکن مخفی دوباره ثبت می‌شود (آدرس همان است). از این به بعد فقط درخواست‌های واقعی تلگرام پذیرفته می‌شوند.

اولین کار زمان‌بندی‌شده‌ی ربات Go یک دقیقه بعد از روشن‌شدنش اجرا می‌شود. تا آن موقع درخواست‌های در جریان PHP تمام شده‌اند و
کرون‌های PHP هم برداشته شده‌اند، پس هیچ کاری (مثلاً تأیید یک رسید) دو بار انجام نمی‌شود.

### بعد از انتقال

- لاگ: `journalctl -u nexrabot@7 -f`
- کلیدهای Nexra Panel: `N=7 bash install.sh keys`
- گوشی تأیید خودکار **همان‌طور که بود کار می‌کند** (همان کلید و همان آدرس `/autopay.php`).
- اسکریپت بکاپ ساعتی (`/root/bot7_backup.sh`) همان‌طور کار می‌کند.
- پوشه‌ی PHP (`/var/www/html/botmirzapanel7`) پاک نمی‌شود؛ برای بازگشت لازم است.

## بازگشت به PHP

```bash
N=7 bash install.sh rollback
```

سایت nginx دقیقاً به فایل قبلی برمی‌گردد و reload می‌شود. وقتی PHP جواب داد، ربات Go خاموش می‌شود و خط‌های کرون PHP به crontab
برمی‌گردند. دیتابیس همان است، پس هر چه در این مدت در ربات Go اتفاق افتاده (خرید، شارژ، کاربر جدید) در PHP هم هست.

چیزهایی که فقط ربات Go دارد و PHP نادیده‌شان می‌گیرد: چیدمان/رنگ دکمه‌ها، صف پیام همگانی در جریان و تصویر رسیدها برای پنل. اگر در ربات
Go کرونی را روشن/خاموش کرده باشید، با بازگشت همان وضعیت crontab زمان انتقال برمی‌گردد.

## آزمون پیش از انتقال (اختیاری)

برای دیدن ربات Go روی کپی داده‌ها، بدون دست‌زدن به ربات اصلی:

```bash
nexrabot db-dump -c /etc/nexrabot/bot7.env --out /root/copy.sql.gz      # یا یک بکاپ قبلی
mysql -e "CREATE DATABASE bot7copy CHARACTER SET utf8mb4; GRANT ALL ON bot7copy.* TO '$(grep ^DB_USER= /etc/nexrabot/bot7.env | cut -d= -f2)'@'localhost'"
cp /etc/nexrabot/bot7.env /root/copy.env && sed -i 's/^DB_NAME=.*/DB_NAME=bot7copy/; s/^LISTEN=.*/LISTEN=127.0.0.1:18999/' /root/copy.env
nexrabot import-sql -c /root/copy.env --file /root/copy.sql.gz
NEXRABOT_DISABLE_CRONS=1 nexrabot serve -c /root/copy.env &
curl -H "Authorization: Bearer $(grep ^API_OWNER_KEY /root/copy.env | cut -d= -f2)" http://127.0.0.1:18999/api/v1/stats
```

(این نسخه وبهوک نمی‌گیرد و فقط برای دیدن داده‌ها و API است؛ چون همان توکن را دارد، از آن پرداخت تأیید نکنید یا پیام نفرستید. بعد از آزمون: `kill %1` و `mysql -e "DROP DATABASE bot7copy"`.)

## انتقال دستی

اگر nginx شما چیدمان دیگری دارد (مثلاً ربات زیر یک مسیر مثل `domain/bot` است، یا Apache):

1. `nexrabot migrate-php --php-dir /path/to/bot --out /etc/nexrabot/bot7.env --listen 127.0.0.1:18007 --skip-db`
2. `nexrabot db-dump -c /etc/nexrabot/bot7.env --out /root/bot7.sql.gz`
3. `nexrabot migrate-php --php-dir /path/to/bot --out /etc/nexrabot/bot7.env --listen 127.0.0.1:18007 --clean-crontab=false`
4. `chown root:nexrabot /etc/nexrabot/bot7.env && chmod 640 /etc/nexrabot/bot7.env`
5. خط‌های `/cron/` همین ربات را از `crontab -e` بردارید.
6. `systemctl enable --now nexrabot@7`
7. در وب‌سرور، همه‌ی درخواست‌های این دامنه را به `http://127.0.0.1:18007` بفرستید (هدر `X-Real-IP` را بگذارید) و reload کنید.
8. `curl https://domain/healthz` باید `ok nexrabot …` بدهد؛ بعد `nexrabot set-webhook -c /etc/nexrabot/bot7.env`.

ربات Go مسیرهای زیرشاخه را نمی‌شناسد؛ برای ربات زیر مسیر، در nginx پیشوند را حذف کنید (`proxy_pass http://127.0.0.1:18007/;` در
`location /bot/`) و آدرس وبهوک را با `setWebhook` خودتان تنظیم کنید.

## از یک بکاپ (سرور جدید)

```bash
N=7 TOKEN=… DOMAIN=… ADMIN=… NEXRA_SECRET=… CERT_EMAIL=… bash install.sh     # ربات تازه
nexrabot import-sql -c /etc/nexrabot/bot7.env --file mirzabot7_backup.sql.gz --force
systemctl restart nexrabot@7
```

`import-sql` روی دیتابیسی که کاربر دارد بدون `--force` کار نمی‌کند، تا اشتباهی چیزی پاک نشود.
