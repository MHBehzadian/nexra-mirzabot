#!/usr/bin/env python3
"""Card to card with the copy buttons on: the message shows tomans and the
"copy amount" button copies rials (bank apps take rials), with and without
the SMS check (which shows a reserved, slightly different amount).

Run after run.py (uses the Go database it leaves behind).
"""
import json
import os
import re
import subprocess
import sys
import time

import pymysql

from menu_test import BOT, BIN, HERE, MOCK, TOKEN, WORK, api, calls, check, http, update

FAILS = []


def copy_button(log):
    for p in calls(log, "editmessagetext") + calls(log, "sendmessage"):
        kb = p.get("reply_markup")
        kb = json.loads(kb) if isinstance(kb, str) else kb
        for row in (kb or {}).get("inline_keyboard", []):
            for b in row:
                if "copy_text" in b and "مبلغ" in b.get("text", ""):
                    return p.get("text", ""), b
    return None, None


def toman_in(text):
    m = re.search(r"مبلغ ([\d,]+)\s+تومان", text)
    return int(m.group(1).replace(",", "")) if m else None


def flow(amount):
    update(msg="/start")
    update(msg="💰 افزایش موجودی")
    update(msg=str(amount))
    return copy_button(update(data="cart_to_offline"))


def main():
    db = pymysql.connect(unix_socket="/var/run/mysqld/mysqld.sock", user="root", database="difgo", autocommit=True)
    cur = db.cursor()
    cur.execute("UPDATE setting SET copy_cart = '1'")
    cur.execute("UPDATE PaySetting SET ValuePay = 'oncard' WHERE NamePay = 'Cartstatus'")
    procs = [subprocess.Popen([sys.executable, os.path.join(HERE, "mock.py"), "9202"]),
             subprocess.Popen([BIN, "serve", "-c", os.path.join(WORK, "go.env")],
                              stdout=open(os.path.join(WORK, "copy.log"), "w"), stderr=subprocess.STDOUT)]
    try:
        for _ in range(100):
            try:
                http("GET", BOT + "/healthz")
                break
            except Exception:
                time.sleep(0.1)
        time.sleep(0.5)
        for mode in ("off", "sms"):
            code, b = api("PUT", "/autopay", {"mode": mode})
            check("autopay " + mode, code == 200, b)
            text, btn = flow(150000)
            check(mode + ": copy buttons shown", btn is not None, text)
            if btn:
                toman = toman_in(text)
                check(mode + ": the button says rials", "ریال" in btn["text"], btn)
                check(mode + ": copies the amount in rials (toman x 10)",
                      toman and btn["copy_text"]["text"] == str(toman * 10), (toman, btn))
        api("PUT", "/autopay", {"mode": "off"})
    finally:
        cur.execute("UPDATE setting SET copy_cart = '0'")
        for p in procs:
            p.terminate()
    import menu_test
    FAILS.extend(menu_test.FAILS)
    print("\n%d failures" % len(FAILS))
    sys.exit(1 if FAILS else 0)


if __name__ == "__main__":
    main()
