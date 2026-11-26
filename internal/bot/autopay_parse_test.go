package bot

import "testing"

func TestParseSMSVariants(t *testing.T) {
	cases := []struct {
		name, sms, dir string
		amount         int64
	}{
		{"persian letters", "بانک ملت\nواریز:۱۲۵,۳۰۰ ریال\nمانده:۵,۴۳۲,۱۰۰ ریال", "in", 12530},
		{"arabic yeh", "مهر ايران\nواريز 1,500,000 ريال\nمانده 9,000,000 ريال", "in", 150000},
		{"arabic yeh, transfer", "انتقال از حساب\nمبلغ 2,000,000 ريال\nمانده: 3,000,000", "in", 200000},
		{"plus sign before", "B.QMEHRIRAN\nحساب 400112345\n+1,500,000\nمانده 7,500,000\n1405/09/05-12:30", "in", 150000},
		{"plus sign after", "حساب 4001\n1,500,000+\nمانده 7,500,000", "in", 150000},
		{"plus after colon", "مبلغ:+1,500,000 ريال\nمانده:7,500,000", "in", 150000},
		{"minus sign", "حساب 4001\n-500,000\nمانده 7,000,000\n1405/09/05-12:30", "out", 50000},
		{"withdrawal word", "برداشت 500,000 ريال\nمانده 1,000,000", "out", 50000},
		{"phone is not money", "تماس +989121234567\nمانده 1,000,000", "unknown", 0},
		{"zero-width joiner", "واریز‌به حساب 1,000,000 ریال", "in", 100000},
	}
	for _, c := range cases {
		p := ParseSMS(c.sms)
		if p.Direction != c.dir || (c.amount != 0 && p.Amount != c.amount) {
			t.Errorf("%s: got %s %d (candidates %v), want %s %d", c.name, p.Direction, p.Amount, p.Candidates, c.dir, c.amount)
		}
	}
}
