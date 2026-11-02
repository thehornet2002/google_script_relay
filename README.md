<div align="center">

# ⚡ Xray Google Script Relay (XGSR)

**پلتفرم قدرتمند ضد فیلترینگ با دامین فرانتینگ گوگل، هسته داخلی Xray و بهینه‌ساز خودکار ماتریسی**  
*High-Performance Anti-Censorship Relay via Google Apps Script & FCM Domain Fronting*

[![GitHub Release](https://img.shields.io/github/v/release/thehornet2002/google_script_relay?style=for-the-badge&color=blue)](https://github.com/thehornet2002/google_script_relay/releases)
[![Go Version](https://img.shields.io/github/go-mod/go-version/thehornet2002/google_script_relay?style=for-the-badge&color=00ADD8)](https://go.dev/)
[![Platform](https://img.shields.io/badge/Platform-Windows%20%7C%20Linux%20%7C%20macOS-informational?style=for-the-badge)](https://github.com/thehornet2002/google_script_relay/releases)
[![License](https://img.shields.io/badge/License-MIT-green?style=for-the-badge)](LICENSE)

[**دانلود آخرین نسخه (Releases)**](https://github.com/thehornet2002/google_script_relay/releases/latest) • [**راهنمای راه‌اندازی سریع**](#-راهنمای-راه‌اندازی-در-۳-گام) • [**معماری شبکه**](#-معماری-سیستم) • [**English Overview**](#-english-overview)

---

</div>

## 📖 فهرست مطالب
- [معماری سیستم](#-معماری-سیستم)
- [ویژگی‌های کلیدی](#-ویژگی‌های-کلیدی)
- [راهنمای راه‌اندازی در ۳ گام](#-راهنمای-راه‌اندازی-در-۳-گام)
  - [گام ۱: راه‌اندازی سرور VPS](#گام-۱-راه‌اندازی-سرور-vps)
  - [گام ۲: ایجاد اسکریپت رله در Google Apps Script](#گام-۲-ایجاد-اسکریپت-رله-در-google-apps-script)
  - [گام ۳: اجرای کلاینت (ویندوز، لینوکس یا مک)](#گام-۳-اجرای-کلاینت-ویندوز-لینوکس-یا-مک)
- [آزمایشگاه بهینه‌سازی دوسطحی ماتریسی (AI Optimizer)](#-آزمایشگاه-بهینه‌سازی-دوسطحی-ماتریسی-ai-optimizer)
- [جدول بسته‌های دانلودی (Release Assets)](#-جدول-بسته‌های-دانلودی-release-assets)
- [رفع اشکال و سوالات متداول (FAQ)](#-رفع-اشکال-و-سوالات-متداول-faq)
- [English Overview](#-english-overview)

---

## 🏗 معماری سیستم

این سیستم ترافیک پروکسی را در قالب درخواست‌های امن HTTPS به زیرساخت ابری **Google Front-End (GFE)** ارسال می‌کند. سیستم فیلترینگ تنها ارتباط کلاینت با سرورهای نوتیفیکیشن اندروید (`fcm.googleapis.com` یا `www.google.com`) را مشاهده می‌کند و به دلیل حضور دامنه‌های حیاتی گوگل در لیست سفید (Too Big to Block)، اتصال حتی در زمان قطع سراسری اینترنت و اینترنت ملی پایدار می‌ماند:

```text
┌────────────────────────────────────────────────────────────────────────────────────────┐
│                                   CLIENT DEVICE (PC/Mac/Linux)                         │
│                                                                                        │
│  [Browser / Applications] ──(10808 SOCKS5 / 10809 HTTP)──> [Embedded Xray Core]       │
│                                                                   │ (Local VLESS/TCP)  │
│                                                                   ▼                    │
│                                                       [Go Relay Client Engine]         │
│                                                       ├── Multi-Account Pool & Health  │
│                                                       ├── Dual Pipelined Sliding-Win   │
│                                                       └── Domain Fronting (FCM SNI)    │
└───────────────────────────────────────────────────────────────────┬────────────────────┘
                                                                    │ (HTTPS / HTTP/2)
                                                                    ▼
┌────────────────────────────────────────────────────────────────────────────────────────┐
│                              GOOGLE APPS SCRIPT RELAY POOL                             │
│                           (script.google.com / GFE Anycast CDN)                        │
└───────────────────────────────────────────────────────────────────┬────────────────────┘
                                                                    │ (HTTP Tunnel Forward)
                                                                    ▼
┌────────────────────────────────────────────────────────────────────────────────────────┐
│                                      LINUX VPS SERVER                                  │
│                                                                                        │
│  [Go Relay Server Engine (:8888)]                                                      │
│  ├── Remote Admin & Dynamic Parameter Tuning                                           │
│  ├── High-Precision Nanosecond Ping Echo                                               │
│  ├── Direct 4MB In-Memory Test Stream (with CRC32)                                     │
│  └── Self-Configuring & Auto-Starting Xray Core Inbound (127.0.0.1:10001)               │
│                                           │ (10001 TCP)                                │
│                                           ▼                                            │
│                               [VPS Inbound (VLESS/TCP)] ───> [Global Internet]         │
└────────────────────────────────────────────────────────────────────────────────────────┘
```

---

## ✨ ویژگی‌های کلیدی

### 🛡️ دور زدن فیلترینگ شدید و اینترنت ملی
با بهره‌گیری از **Domain Fronting** روی سرورهای گوگل پوش (`fcm.googleapis.com` / `mtalk.google.com`)، درخواست‌ها مانند تبادل نوتیفیکیشن رسمی اندروید رفتار کرده و مسدود نمی‌شوند.

### ⚡ هسته داخلی Xray Core (بی‌نیاز از v2rayN یا تنظیمات دستی)
- کلاینت دارای هسته داخلی `xray.exe` است و پس از کلیک روی استارت، به طور خودکار پورت‌های **SOCKS5 روی `10808`** و **HTTP روی `10809`** را باز می‌کند.
- **دکمه تک‌کلیکه Windows Proxy:** با یک کلیک پروکسی کل ویندوز و مرورگرها فعال می‌شود.

### 🚀 پنجره لغزان دوکاناله (Dual-Pipelined Sliding Window)
تونل‌های وب سنتی تک‌مرحله‌ای (Stop-and-Wait) هستند؛ این برنامه با اجرای **دو ورکر همزمان**، شماره‌گذاری سریال پکت‌ها (`Seq`) و بافر مرتب‌سازی، زمان مرده ۱.۵ ثانیه‌ای وب را حذف کرده و سرعت دانلود را **۳ الی ۴ برابر** می‌کند.

### 🤖 آزمایشگاه بهینه‌سازی دوسطحی ماتریسی (AI Optimizer)
سیستم دارای موتور خودکار ارزیابی پارامترها بر اساس پروب‌های نانوثانیه اختصاصی و دانلود استریم ۴ مگابایتی مستقیم از رم سرور VPS (همراه با چکسام CRC32) است و به صورت خودکار بهترین پیکربندی را هم در کلاینت و هم در سرور ثبت می‌کند.

### 🗂️ استخر چند اکانته و سوییچ خودکار (Failover & Balance)
با وارد کردن چندین لینک گوگل اسکریپت (هر خط یک اکانت)، ترافیک بر اساس الگوریتم **کمترین درخواست در جریان (Least In-Flight)** تقسیم می‌شود و اگر اکانتی به سقف روزانه گوگل برسد، بدون قطعی به اکانت بعدی سوییچ می‌شود.

### 🖥️ رابط کاربری چندسکویی (ویندوز، لینوکس و مک)
- **ویندوز:** رابط کاربری مدرن با تم تیره عمیق (Immersive Dark Mode)، فونت Segoe UI، کنترل‌های تب‌بندی شده و مانیفست Windows Common Controls v6.
- **لینوکس:** نسخه دسکتاپ مستقل با شورتکات رسمی `.desktop` برای محیط‌های گنوم، کی‌دی‌ای و غیره.
- **مک‌او‌اس:** نسخه بهینه‌شده برای هر دو پردازنده Intel و Apple Silicon (M1/M2/M3/M4).

### 🔧 سرور خودپیکربند (Self-Configuring VPS Server)
سرور لینوکس پورت اینباند `10001` را بررسی می‌کند؛ اگر اینباندی وجود نداشته باشد، **به طور خودکار کانفیگ استاندارد VLESS را ساخته و هسته Xray سرور را استارت می‌کند.** نیازی به نصب پنل یا کانفیگ دستی سرور نیست!

---

## 🚀 راهنمای راه‌اندازی در ۳ گام

### گام ۱: راه‌اندازی سرور VPS

1. از بخش [Releases](https://github.com/thehornet2002/google_script_relay/releases/latest)، بسته **`relay-server-linux-amd64.tar.gz`** (یا نسخه arm64 برای سرورهای ARM) را دانلود کرده و به سرور منتقل کنید:
   ```bash
   mkdir -p /root/google_script
   cd /root/google_script
   tar -xzvf relay-server-linux-amd64.tar.gz
   chmod +x relay
   ```

2. باز کردن پورت فایروال:
   ```bash
   sudo ufw allow 22/tcp
   sudo ufw allow 8888/tcp
   sudo ufw enable
   ```

3. نصب و اجرای سرویس دائمی systemd:
   ```bash
   sudo cp relay.service /etc/systemd/system/relay.service
   sudo systemctl daemon-reload
   sudo systemctl enable --now relay
   sudo systemctl status relay
   ```
   *(سرور به طور خودکار اینباند Xray روی پورت ۱۰001 را راه‌اندازی می‌کند).*

---

### گام ۲: ایجاد اسکریپت رله در Google Apps Script

1. با مرورگر (ترجیحاً در حالت ناشناس/Incognito) وارد [script.google.com](https://script.google.com) شوید و پروژه جدید ایجاد کنید.
2. محتوای فایل [`gas/Code.gs`](gas/Code.gs) را در ویرایشگر کپی کنید.
3. در خط اول فایل، IP سرور خود را جایگزین کنید:
   ```javascript
   var VPS_URL = "http://YOUR_VPS_IP:8888/tunnel";
   ```
4. دکمه **Deploy** > **New deployment** را بزنید:
   - نوع: **Web app**
   - Execute as: **Me**
   - Who has access: حتماً **`Anyone`** باشد.
5. دکمه **Deploy** را زده و لینک URL وب‌اپلیکیشن تولیدشده را کپی کنید (`https://script.google.com/macros/s/.../exec`).
6. *(اختیاری ولی توصیه شده):* برای سرعت بیشتر و تقسیم بار، همین کار را در ۲ یا ۳ اکانت جیمیل دیگر نیز تکرار کنید و لینک‌های آن‌ها را نگه دارید.

---

### گام ۳: اجرای کلاینت (ویندوز، لینوکس یا مک)

#### در ویندوز:
1. فایل **`relay-gui-windows-amd64.zip`** را دانلود و اکسترکت کنید.
2. روی **`relay-gui.exe`** کلیک کنید.
3. در تب **🌐 Google Script Accounts**، لینک‌های مرحله قبل را وارد کنید (هر خط یک لینک) و توکن مشترک سرور (`IK13eIodrB24`) را چک کنید.
4. در تب اول، دکمه **▶ Start Relay + Xray** را بزنید.
5. دکمه **🛡 Windows Proxy: ON** را بزنید تا کل ترافیک سیستم وصل شود.

#### در لینوکس یا مک:
1. بسته **`relay-gui-linux-amd64.tar.gz`** یا **`relay-gui-darwin-*.tar.gz`** را اکسترکت کرده و اجرا کنید:
   ```bash
   ./relay-gui
   ```

---

## 🧪 آزمایشگاه بهینه‌سازی دوسطحی ماتریسی (AI Optimizer)

در پنجره کلاینت وارد تب **⚡ AI Matrix Test Lab** شوید و یکی از ۳ استراتژی را انتخاب کنید:

| استراتژی | تمرکز بهینه‌سازی | کاربرد اصلی |
| :--- | :--- | :--- |
| **1. Best Ping** | حداقل تأخیر میانه، حذف Jitter، کاهش زمان نگه‌داشت به ۲۰۰ms | بازی‌های آنلاین، تماس صوتی/تصویری، وب‌گردی فوق‌سریع |
| **2. Best Speed** | حداکثر پهنای باند بافرها (۲۵۶KB) و ورکر‌های پایپ‌لاین ۳ و ۴ | استریم ویدیوی 4K، دانلود پرسرعت، یوتیوب |
| **3. Balanced** | بهینه‌سازی نسبت حاصل‌ضرب تأخیر در پهنای باند (BDP) | وب‌گردی روزمره و دانلود متعادل |

سپس روی **Run Two-Stage Matrix Optimization** کلیک کنید:
1. سیستم در فاز اول ۶ معماری پارامتری را می‌سنجد.
2. در فاز دوم پارامترهای برنده را با دقت ریز (Micro-tuning) بهینه‌تر می‌کند.
3. پروب‌های پینگ اختصاصی و دانلود ۴ مگابایتی واقعی از سرور گرفته می‌شود.
4. **تنظیمات برنده به طور خودکار هم روی کامپیوتر و هم روی سرور VPS ذخیره و قفل می‌شود!**

---

## 📦 جدول بسته‌های دانلودی (Release Assets)

تمامی نسخه‌ها توسط [GitHub Actions](https://github.com/thehornet2002/google_script_relay/actions) به صورت خودکار کامپایل و در هر ریلیز قرار می‌گیرند:

| نوع بسته | نام فایل | سیستم‌عامل و معماری | توضیحات |
| :--- | :--- | :--- | :--- |
| **Windows GUI** | `relay-gui-windows-amd64.zip` | Windows (x64) | برنامه گرافیکی تب‌بندی شده + هسته رسمی Xray |
| **Linux GUI** | `relay-gui-linux-amd64.tar.gz` | Linux Desktop (x86_64) | برنامه گرافیکی مستقل لینوکس + فایل `.desktop` |
| **Linux GUI ARM** | `relay-gui-linux-arm64.tar.gz` | Linux Desktop (ARM64) | برنامه گرافیکی برای بردهای ARM لینوکس |
| **macOS GUI Intel** | `relay-gui-darwin-amd64.tar.gz` | macOS (Intel x86_64) | برنامه گرافیکی برای مک‌های اینتل |
| **macOS GUI Apple** | `relay-gui-darwin-arm64.tar.gz` | macOS (Apple Silicon M1-M4) | برنامه گرافیکی بومی برای مک‌های سری M |
| **Linux Server** | `relay-server-linux-amd64.tar.gz`| Linux Server (x86_64) | باینری سرور VPS + سرویس `relay.service` |
| **Linux Server ARM**| `relay-server-linux-arm64.tar.gz`| Linux Server (ARM64) | باینری سرور برای VPSهای اوراکل ARM |
| **Linux Server ARMv7**| `relay-server-linux-armv7.tar.gz`| Linux Server (ARMv7) | باینری سرور سبک برای بردهای کم‌مصرف |
| **Windows CLI** | `relay-cli-windows-amd64.zip` | Windows (x64) | نسخه ترمینال برای اسکریپت‌ها و خودکارسازی |
| **Windows CLI ARM** | `relay-cli-windows-arm64.zip` | Windows (ARM64) | نسخه ترمینال برای سرفیس و ویندوزهای ARM |
| **macOS CLI** | `relay-cli-darwin-*.tar.gz` | macOS (x64 / ARM64) | نسخه ترمینال برای مک |
| **Checksums** | `sha256sums.txt` | All Platforms | هشدارهای SHA-256 برای راستی‌آزمایی فایل‌ها |

---

## ❓ رفع اشکال و سوالات متداول (FAQ)

### پیام "به دسترسی نیاز دارید" یا خطای ۴۰۳ در گوگل اسکریپت چیست؟
در ادیتور Google Apps Script دکمه **Deploy** > **Manage deployments** را بزنید، روی آیکون مداد کلیک کنید و گزینه **Who has access** را حتماً روی **`Anyone`** قرار داده و مجدد دیپلوی کنید.

### آیا این روش نیاز به کارت اعتباری بین‌المللی دارد؟
**خیر، ۱۰۰٪ رایگان است.** هیچ‌کدام از بخش‌های Google Apps Script نیازی به کردیت کارت یا اکانت‌های پولی ندارند.

### آیا روی لینوکس سرور نیاز به نصب پنل X-UI یا 3X-UI دارم؟
خیر! برنامه سرور `relay-server-linux-*` کامپایل شده است تا در صورت نبود اینباند روی پورت `10001`، فایل کانفیگ اختصاصی VLESS/TCP را خودکار ایجاد کرده و سرور Xray را در پس‌زمینه اجرا کند.

---

## 🌐 English Overview

**Xray Google Script Relay (XGSR)** is an enterprise-grade anti-censorship platform engineered to bypass deep packet inspection (DPI) and nationwide whitelisting filters using **Google Apps Script** as a resilient relay with **FCM / Google Push Domain Fronting**.

### Key Architectural Capabilities:
- **Zero-Blockability:** Traffic masquerades as native Android Google Push/FCM notification traffic to `fcm.googleapis.com` / `www.google.com`.
- **Embedded Xray Core:** Built-in Xray lifecycle manager exposing local SOCKS5 (`10808`) and HTTP (`10809`) with single-click system proxy toggle.
- **Dual-Pipelined Sliding Window:** Eliminates traditional Stop-and-Wait HTTP long-poll dead times, yielding 3-4x throughput over standard HTTP tunnels.
- **Two-Stage AI Matrix Optimizer:** Automatically explores parameter spaces through direct VPS nanosecond probes and 4MB CRC32-verified download tests to lock in optimal settings.
- **Self-Configuring VPS Server:** Automatically creates and operates a local VLESS/TCP inbound on port `10001` without requiring manual panel configurations.
- **Multi-Platform Desktop GUI:** Native dark-themed interfaces for Windows (Win32 Common Controls v6 with High-DPI), Linux (Desktop app mode with `.desktop` launcher), and macOS (Universal Intel & Apple Silicon).

---

## 📄 مجوز (License)
این پروژه تحت مجوز [MIT License](LICENSE) منتشر شده است.
