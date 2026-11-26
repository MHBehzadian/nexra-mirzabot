package site.nexra.autopay

import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.provider.Telephony
import java.util.Date

class SmsReceiver : BroadcastReceiver() {

    companion object {
        private val MONEY_WORDS = listOf(
            "واریز", "بستانکار", "برداشت", "بدهکار", "مانده", "موجودی",
            "ریال", "تومان", "انتقال", "حساب", "کارت", "مبلغ", "بانک"
        )

        // an amount with only a sign: "+1,500,000" or "1,500,000+"
        private val SIGNED_AMOUNT = Regex("(^|[\\s:])[+\\-−]\\s?[0-9۰-۹][0-9۰-۹,٬،]{2,}|[0-9۰-۹][0-9۰-۹,٬،]{2,}\\s?[+\\-−]($|\\s)")

        /** Arabic-keyboard letters the Persian way ("واريز" → "واریز"), no invisible joiners. */
        fun normalize(s: String): String = s
            .replace('ي', 'ی').replace('ى', 'ی').replace('ك', 'ک')
            .replace("\u200c", " ").replace("\u200d", "").replace("\u200e", "").replace("\u200f", "")

        /**
         * Keeps the phone's private messages on the phone: only something that
         * reads like a bank notification is ever queued, and only after the
         * filter the admin set, when they set one.
         */
        fun looksLikeBank(raw: String): Boolean {
            if (raw.isBlank()) return false
            val body = normalize(raw)
            val hits = MONEY_WORDS.count { body.contains(it) }
            val hasNumber = Regex("[0-9۰-۹]{3,}").containsMatchIn(body.replace(",", "").replace("٬", ""))
            return hasNumber && (hits >= 2 || (hits >= 1 && SIGNED_AMOUNT.containsMatchIn(body)))
        }

        /** "B.QMEHRIRAN", "b qmehriran" and "BQMEHRIRAN" are one sender. */
        private fun senderKey(s: String) = s.filter { it.isLetterOrDigit() }.lowercase()

        fun senderAllowed(ctx: Context, sender: String): Boolean {
            val filter = Prefs.senderFilter(ctx).trim()
            if (filter.isEmpty()) return true
            val key = senderKey(sender)
            return filter.split(",", "،", "\n")
                .map { senderKey(it) }
                .filter { it.isNotEmpty() }
                .any { key.contains(it) }
        }
    }

    override fun onReceive(ctx: Context, intent: Intent) {
        if (intent.action != Telephony.Sms.Intents.SMS_RECEIVED_ACTION) return
        if (!Prefs.paired(ctx)) return

        // a long SMS arrives in parts; they belong to one message
        val parts = Telephony.Sms.Intents.getMessagesFromIntent(intent) ?: return
        if (parts.isEmpty()) return
        val sender = parts[0].originatingAddress ?: ""
        val body = parts.joinToString("") { it.messageBody ?: "" }
        val at = ForwardService.stamp.format(Date(parts[0].timestampMillis))

        val store = Store(ctx)
        // say why a bank-looking message, or one from the filtered sender, was
        // left out (never its text), so a wrong filter shows up in the log
        val bank = looksLikeBank(body)
        val allowed = senderAllowed(ctx, sender)
        if (!allowed) {
            if (bank) store.log("پیامک از $sender فرستاده نشد: این فرستنده در فیلتر نیست")
            return
        }
        if (!bank) {
            if (Prefs.senderFilter(ctx).isNotBlank()) store.log("پیامک از $sender فرستاده نشد: شبیه پیامک واریز نبود")
            return
        }

        if (store.enqueue(sender, body, at)) {
            store.log("پیامک تازه از $sender")
        }
        ForwardService.flush(ctx)
    }
}
