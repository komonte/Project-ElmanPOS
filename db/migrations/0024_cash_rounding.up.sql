-- 0024_cash_rounding.up.sql
-- Cash rounding: the part of a sale paid in cash is rounded DOWN to a
-- multiple of this setting (100: $12,345 in cash is charged $12,300). Card,
-- transfer and QR are never rounded. The difference is the sale's
-- discount_amount (total = subtotal - discount_amount), so the expected cash
-- of a close already counts the rounded amount. 0 turns it off. A cash part
-- smaller than the multiple is not rounded (it would be free).
INSERT INTO app_settings (key, value, description)
VALUES ('cash_rounding_multiple', '100'::jsonb,
        'Cash rounding: the cash part of a sale is rounded down to this multiple (0 = off)')
ON CONFLICT (key) DO NOTHING;
