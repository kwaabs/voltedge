-- 0179_trouble_call_premise.sql
-- Purpose: CIS/IVR inbound (blocker #5) — let a customer outage report carry the
-- caller's PREMISE identity and callback number, so a report filed by an external
-- IVR/CIS can be tied back to its customer and (later) called back when supply
-- returns. The utility's customer model lives in the external CIS; VoltEdge only
-- records the two refs a report brings and passes them through:
--
--   account_ref — the CIS customer / premise / account id (opaque to us; the CIS
--                 maps it to its own customer record).
--   phone       — a callback number the caller left, for the restoration callback.
--
-- Both nullable: a phoned-in call logged by an operator, an AMI last-gasp, or a map
-- click carries neither; only an IVR/CIS customer_report typically fills them.

ALTER TABLE oms.trouble_calls ADD COLUMN IF NOT EXISTS account_ref text;
ALTER TABLE oms.trouble_calls ADD COLUMN IF NOT EXISTS phone       text;

COMMENT ON COLUMN oms.trouble_calls.account_ref IS 'CIS customer/premise/account id from an IVR/CIS report (0179); opaque — the external CIS owns the customer model.';
COMMENT ON COLUMN oms.trouble_calls.phone IS 'Callback number the caller left (0179), carried through to the restoration callback queue.';

-- Resolve a premise''s open calls / the account behind a service point.
CREATE INDEX IF NOT EXISTS trouble_calls_account_ref_idx
    ON oms.trouble_calls (account_ref) WHERE account_ref IS NOT NULL;
