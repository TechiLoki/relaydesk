\set ON_ERROR_STOP on
BEGIN;
-- Run in a development stack with the worker stopped. This transaction is rolled back.
UPDATE internal.deliveries SET status='failed',lease_token=NULL,leased_until=NULL WHERE status IN ('pending','retry_wait','processing');
DO $$
DECLARE payload jsonb; result jsonb; replay jsonb; oid uuid; did uuid; j jsonb; receipt jsonb; token uuid; n integer; state text; dest_id uuid;
BEGIN
 payload:='{"source":"demo","external_id":"SQL-TEST","version":1,"customer":"Test","email":"test@example.test","currency":"USD","amount_minor":100,"shipping_minor":0,"tax_minor":0,"discount_minor":0,"items":[{"sku":"A","name":"A","quantity":1,"unit_amount_minor":100}],"validation":{"subtotal_minor":100,"total_minor":100,"units":1,"checked_lines":true}}';
 result:=api.ingest_event('demo','evt_sql_test',repeat('a',64),payload,NULL);
 oid:=(result->>'order_id')::uuid;
 replay:=api.ingest_event('demo','evt_sql_test',repeat('a',64),payload,NULL);
 IF replay->>'duplicate'<>'true' THEN RAISE EXCEPTION 'Replay not recognized'; END IF;
 SELECT count(*) INTO n FROM internal.deliveries WHERE order_id=oid;
 IF n<>1 THEN RAISE EXCEPTION 'Duplicate delivery queued'; END IF;
 BEGIN
  PERFORM api.ingest_event('demo','evt_sql_test',repeat('b',64),payload,NULL);
  RAISE EXCEPTION 'Reused event ID accepted';
 EXCEPTION WHEN unique_violation THEN NULL; END;
 result:=api.ingest_event('demo','evt_sql_equivalent',repeat('c',64),payload,NULL);
 IF result->>'status'<>'duplicate' THEN RAISE EXCEPTION 'Equivalent business state was not deduplicated'; END IF;
 j:=api.claim_delivery();did:=(j->>'id')::uuid;token:=(j->>'token')::uuid;
 IF j->>'order_id'<>oid::text THEN RAISE EXCEPTION 'Wrong delivery claimed'; END IF;
 IF api.claim_delivery() IS NOT NULL THEN RAISE EXCEPTION 'Active lease claimed twice'; END IF;
 IF api.finish_delivery(did,gen_random_uuid(),200,1,NULL,false,0) THEN RAISE EXCEPTION 'Wrong lease accepted'; END IF;
 PERFORM api.set_destination_mode('lost_ack');
 receipt:=api.accept_delivery(did,payload);
 IF receipt->>'http_status'<>'503' THEN RAISE EXCEPTION 'Lost acknowledgement was not simulated'; END IF;
 SELECT id INTO dest_id FROM internal.destination_orders WHERE source='demo' AND external_id='SQL-TEST';
 IF dest_id IS NULL THEN RAISE EXCEPTION 'Lost acknowledgement did not commit'; END IF;
 receipt:=api.accept_delivery(did,payload);
 IF receipt->>'duplicate'<>'true' THEN RAISE EXCEPTION 'Destination idempotency failed'; END IF;
 SELECT count(*) INTO n FROM internal.destination_receipts WHERE idempotency_key=did;
 IF n<>1 THEN RAISE EXCEPTION 'Duplicate receipt'; END IF;
 BEGIN
  PERFORM api.accept_delivery(did,jsonb_set(payload,'{amount_minor}','200'));
  RAISE EXCEPTION 'Conflicting idempotency payload accepted';
 EXCEPTION WHEN unique_violation THEN NULL; END;
 PERFORM api.finish_delivery(did,token,503,10,'Lost reply',true,0);
 j:=api.claim_delivery();token:=(j->>'token')::uuid;
 IF api.finish_delivery(did,token,200,10,NULL,false,0) IS DISTINCT FROM true THEN RAISE EXCEPTION 'Success not committed'; END IF;
 IF api.finish_delivery(did,token,200,10,NULL,false,0) THEN RAISE EXCEPTION 'Duplicate completion accepted'; END IF;
 -- A newer version must survive a later arrival of an older delivery.
 payload:=jsonb_set(payload,'{version}','2');
 PERFORM api.ingest_event('demo','evt_sql_v2',repeat('d',64),payload,NULL);
 receipt:=api.accept_delivery(gen_random_uuid(),payload);
 receipt:=api.accept_delivery(gen_random_uuid(),jsonb_set(payload,'{version}','1'));
 IF receipt->>'ignored_stale'<>'true' THEN RAISE EXCEPTION 'Stale destination version not ignored'; END IF;
 SELECT version INTO n FROM internal.destination_orders WHERE id=dest_id;
 IF n<>2 THEN RAISE EXCEPTION 'Destination was downgraded'; END IF;
 result:=api.ingest_event('demo','evt_sql_stale',repeat('e',64),jsonb_set(payload,'{version}','1'),NULL);
 IF result->>'status'<>'stale' THEN RAISE EXCEPTION 'Stale source version queued'; END IF;
 j:=api.claim_delivery();did:=(j->>'id')::uuid;token:=(j->>'token')::uuid;
 UPDATE internal.deliveries SET leased_until=now()-interval '1 second' WHERE id=did;
 j:=api.claim_delivery();
 IF (j->>'token')::uuid=token THEN RAISE EXCEPTION 'Lease recovery did not fence old worker'; END IF;
 IF api.finish_delivery(did,token,200,1,NULL,false,0) THEN RAISE EXCEPTION 'Expired worker committed'; END IF;
 token:=(j->>'token')::uuid;
 PERFORM api.finish_delivery(did,token,401,1,'Bad destination credential',false,0);
 SELECT status INTO state FROM internal.deliveries WHERE id=did;
 IF state<>'failed' THEN RAISE EXCEPTION 'Permanent error was retried'; END IF;
 PERFORM api.retry_delivery(did);
 j:=api.claim_delivery();
 IF (j->>'attempt')::int<>3 THEN RAISE EXCEPTION 'Manual retry reset lifetime attempts'; END IF;
 -- Crash on final attempt must eventually dead-letter rather than hang.
 UPDATE internal.deliveries SET cycle_attempts=max_attempts,leased_until=now()-interval '1 second' WHERE id=did;
 PERFORM api.claim_delivery();
 SELECT status INTO state FROM internal.deliveries WHERE id=did;
 IF state<>'failed' THEN RAISE EXCEPTION 'Final-attempt lease expiry did not terminate'; END IF;
 IF has_table_privilege('relay_app','internal.orders','INSERT') OR has_table_privilege('relay_app','internal.deliveries','UPDATE') THEN RAISE EXCEPTION 'Direct writes exposed'; END IF;
 IF has_schema_privilege('relay_app','internal','USAGE') THEN RAISE EXCEPTION 'Internal schema exposed'; END IF;
 RAISE NOTICE 'PASS: deduplication, event conflicts, destination idempotency, lost ACK, version ordering, lease fencing, retry, dead-letter and permissions';
END $$;
ROLLBACK;
