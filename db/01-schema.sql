CREATE SCHEMA internal;
CREATE SCHEMA api;
REVOKE CREATE ON SCHEMA public FROM PUBLIC;
ALTER DEFAULT PRIVILEGES IN SCHEMA api REVOKE EXECUTE ON FUNCTIONS FROM PUBLIC;
CREATE TABLE internal.orders (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 source text NOT NULL CHECK(source IN ('demo','stripe')),
 external_id text NOT NULL,
 version bigint NOT NULL CHECK(version>0),
 payload jsonb NOT NULL,
 payload_hash text NOT NULL,
 latest_delivery uuid,
 created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(source,external_id)
);
CREATE TABLE internal.events (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 source text NOT NULL,
 external_id text NOT NULL,
 order_id uuid NOT NULL REFERENCES internal.orders,
 body_hash text NOT NULL,
 status text NOT NULL CHECK(status IN ('accepted','duplicate','stale')),
 duplicate_count integer NOT NULL DEFAULT 0,
 created_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(source,external_id)
);
CREATE TABLE internal.deliveries (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 order_id uuid NOT NULL REFERENCES internal.orders,
 version bigint NOT NULL,
 payload jsonb NOT NULL,
 status text NOT NULL DEFAULT 'pending' CHECK(status IN ('pending','processing','retry_wait','succeeded','failed')),
 attempt_count integer NOT NULL DEFAULT 0,
 cycle_attempts integer NOT NULL DEFAULT 0,
 max_attempts integer NOT NULL DEFAULT 5,
 next_attempt_at timestamptz NOT NULL DEFAULT now(),
 lease_token uuid,
 leased_until timestamptz,
 last_error text,
 created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(order_id,version)
);
ALTER TABLE internal.orders ADD CONSTRAINT orders_delivery_fkey FOREIGN KEY(latest_delivery) REFERENCES internal.deliveries DEFERRABLE INITIALLY DEFERRED;
CREATE INDEX deliveries_queue ON internal.deliveries(next_attempt_at) WHERE status IN ('pending','retry_wait','processing');
CREATE TABLE internal.attempts (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 delivery_id uuid NOT NULL REFERENCES internal.deliveries,
 number integer NOT NULL,
 http_status integer,
 duration_ms integer NOT NULL,
 error text,
 created_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(delivery_id,number)
);
CREATE TABLE internal.audit (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 order_id uuid REFERENCES internal.orders,
 action text NOT NULL,
 detail jsonb NOT NULL DEFAULT '{}',
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX audit_order ON internal.audit(order_id,id DESC);
CREATE TABLE internal.settings (
 id integer PRIMARY KEY CHECK(id=1),
 destination_mode text NOT NULL DEFAULT 'healthy' CHECK(destination_mode IN ('healthy','outage','lost_ack')),
 fail_once boolean NOT NULL DEFAULT false,
 last_demo_event jsonb,
 worker_seen timestamptz
);
INSERT INTO internal.settings(id) VALUES(1);
CREATE TABLE internal.destination_orders (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 source text NOT NULL,
 external_id text NOT NULL,
 version bigint NOT NULL,
 payload jsonb NOT NULL,
 updated_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(source,external_id)
);
CREATE TABLE internal.destination_receipts (
 idempotency_key uuid PRIMARY KEY,
 payload_hash text NOT NULL,
 source text NOT NULL,
 external_id text NOT NULL,
 version bigint NOT NULL,
 ignored_stale boolean NOT NULL DEFAULT false,
 created_at timestamptz NOT NULL DEFAULT now()
);

CREATE FUNCTION api.ping() RETURNS jsonb LANGUAGE sql STABLE SECURITY DEFINER SET search_path=internal,pg_temp AS $$ SELECT jsonb_build_object('ok',true); $$;

CREATE FUNCTION api.ingest_event(p_source text,p_event_id text,p_body_hash text,p_order jsonb,p_demo_event jsonb DEFAULT NULL) RETURNS jsonb
LANGUAGE plpgsql SECURITY DEFINER SET search_path=internal,pg_temp AS $$
DECLARE previous events; current_order orders; oid uuid; did uuid; version_num bigint; fingerprint text; event_status text := 'accepted';
BEGIN
 IF p_source NOT IN ('demo','stripe') OR p_source IS DISTINCT FROM p_order->>'source' OR p_event_id IS NULL OR length(p_event_id) NOT BETWEEN 1 AND 160 OR p_body_hash !~ '^[0-9a-f]{64}$' THEN RAISE EXCEPTION 'Invalid event fields' USING ERRCODE='22023'; END IF;
 version_num:=(p_order->>'version')::bigint;
 IF version_num<1 OR p_order->>'external_id' IS NULL OR length(p_order->>'external_id') NOT BETWEEN 1 AND 160 OR (p_order->>'amount_minor')::bigint NOT BETWEEN 0 AND 1000000000000 OR p_order->>'currency' !~ '^[A-Z]{3}$' THEN RAISE EXCEPTION 'Invalid normalized order' USING ERRCODE='22023'; END IF;
 fingerprint:=encode(sha256(convert_to(p_order::text,'UTF8')),'hex');
 -- Lock event identity first, then order identity. This covers races before a row exists.
 PERFORM pg_advisory_xact_lock(hashtextextended('event:'||p_source||':'||p_event_id,0));
 SELECT * INTO previous FROM events WHERE source=p_source AND external_id=p_event_id;
 IF FOUND THEN
  IF previous.body_hash<>p_body_hash THEN RAISE EXCEPTION 'Event ID reused with different content' USING ERRCODE='23505'; END IF;
  UPDATE events SET duplicate_count=duplicate_count+1 WHERE id=previous.id;
  INSERT INTO audit(order_id,action,detail) VALUES(previous.order_id,'duplicate_ignored',jsonb_build_object('event',p_event_id));
  RETURN jsonb_build_object('order_id',previous.order_id,'event_id',previous.id,'status','duplicate','duplicate',true);
 END IF;
 PERFORM pg_advisory_xact_lock(hashtextextended('order:'||p_source||':'||(p_order->>'external_id'),0));
 SELECT * INTO current_order FROM orders WHERE source=p_source AND external_id=p_order->>'external_id' FOR UPDATE;
 IF FOUND THEN
  oid:=current_order.id;
  IF version_num<current_order.version THEN event_status:='stale';
  ELSIF version_num=current_order.version THEN
   IF fingerprint<>current_order.payload_hash THEN RAISE EXCEPTION 'Version reused with different order' USING ERRCODE='23505'; END IF;
   event_status:='duplicate';
  ELSE
   UPDATE orders SET version=version_num,payload=p_order,payload_hash=fingerprint,updated_at=now() WHERE id=oid;
  END IF;
 ELSE
  INSERT INTO orders(source,external_id,version,payload,payload_hash) VALUES(p_source,p_order->>'external_id',version_num,p_order,fingerprint) RETURNING id INTO oid;
 END IF;
 INSERT INTO events(source,external_id,order_id,body_hash,status) VALUES(p_source,p_event_id,oid,p_body_hash,event_status) RETURNING id INTO previous.id;
 INSERT INTO audit(order_id,action,detail) VALUES(oid,'event_'||event_status,jsonb_build_object('event',p_event_id,'version',version_num));
 IF event_status='accepted' THEN
  INSERT INTO deliveries(order_id,version,payload) VALUES(oid,version_num,p_order) RETURNING id INTO did;
  UPDATE orders SET latest_delivery=did WHERE id=oid;
  INSERT INTO audit(order_id,action,detail) VALUES(oid,'delivery_queued',jsonb_build_object('delivery_id',did,'version',version_num));
 END IF;
 IF p_source='demo' AND p_demo_event IS NOT NULL AND event_status='accepted' THEN UPDATE settings SET last_demo_event=p_demo_event WHERE id=1; END IF;
 RETURN jsonb_build_object('order_id',oid,'event_id',previous.id,'status',event_status,'duplicate',event_status='duplicate');
END $$;

CREATE FUNCTION api.claim_delivery() RETURNS jsonb
LANGUAGE plpgsql SECURITY DEFINER SET search_path=internal,pg_temp AS $$
DECLARE j deliveries; token uuid;
BEGIN
 UPDATE settings SET worker_seen=clock_timestamp() WHERE id=1;
 -- A crash on the final attempt must not leave a job processing forever.
 WITH exhausted AS (
  UPDATE deliveries SET status='failed',last_error='Worker lease expired on final attempt',updated_at=now(),lease_token=NULL,leased_until=NULL
  WHERE status='processing' AND leased_until<now() AND cycle_attempts>=max_attempts RETURNING order_id,id
 ) INSERT INTO audit(order_id,action,detail) SELECT order_id,'lease_exhausted',jsonb_build_object('delivery_id',id) FROM exhausted;
 SELECT * INTO j FROM deliveries WHERE
 ((status IN ('pending','retry_wait') AND next_attempt_at<=now()) OR (status='processing' AND leased_until<now()))
 AND cycle_attempts<max_attempts ORDER BY next_attempt_at,created_at FOR UPDATE SKIP LOCKED LIMIT 1;
 IF NOT FOUND THEN RETURN NULL; END IF;
 token:=gen_random_uuid();
 UPDATE deliveries SET status='processing',lease_token=token,leased_until=now()+interval '30 seconds',attempt_count=attempt_count+1,cycle_attempts=cycle_attempts+1,updated_at=now() WHERE id=j.id;
 INSERT INTO audit(order_id,action,detail) VALUES(j.order_id,CASE WHEN j.status='processing' THEN 'lease_reclaimed' ELSE 'attempt_started' END,jsonb_build_object('delivery_id',j.id,'attempt',j.attempt_count+1));
 RETURN jsonb_build_object('id',j.id,'order_id',j.order_id,'token',token,'attempt',j.attempt_count+1,'payload',j.payload);
END $$;

CREATE FUNCTION api.finish_delivery(p_id uuid,p_token uuid,p_http_status integer,p_duration_ms integer,p_error text,p_retryable boolean,p_delay_seconds double precision) RETURNS boolean
LANGUAGE plpgsql SECURITY DEFINER SET search_path=internal,pg_temp AS $$
DECLARE j deliveries; next_state text;
BEGIN
 SELECT * INTO j FROM deliveries WHERE id=p_id FOR UPDATE;
 IF NOT FOUND OR j.status<>'processing' OR j.lease_token IS DISTINCT FROM p_token OR j.leased_until<clock_timestamp() THEN RETURN false; END IF;
 IF p_duration_ms NOT BETWEEN 0 AND 60000 OR p_delay_seconds NOT BETWEEN 0 AND 300 THEN RAISE EXCEPTION 'Invalid attempt timing' USING ERRCODE='22023'; END IF;
 IF p_error IS NULL AND p_http_status BETWEEN 200 AND 299 THEN next_state:='succeeded';
 ELSIF p_retryable AND j.cycle_attempts<j.max_attempts THEN next_state:='retry_wait';
 ELSE next_state:='failed'; END IF;
 INSERT INTO attempts(delivery_id,number,http_status,duration_ms,error) VALUES(j.id,j.attempt_count,p_http_status,p_duration_ms,left(p_error,500));
 UPDATE deliveries SET status=next_state,last_error=left(p_error,500),next_attempt_at=now()+p_delay_seconds*interval '1 second',lease_token=NULL,leased_until=NULL,updated_at=now() WHERE id=j.id;
 INSERT INTO audit(order_id,action,detail) VALUES(j.order_id,'delivery_'||next_state,jsonb_build_object('delivery_id',j.id,'attempt',j.attempt_count,'http_status',p_http_status,'error',left(p_error,500)));
 RETURN true;
END $$;

CREATE FUNCTION api.retry_delivery(p_id uuid) RETURNS jsonb
LANGUAGE plpgsql SECURITY DEFINER SET search_path=internal,pg_temp AS $$
DECLARE j deliveries;
BEGIN
 SELECT * INTO j FROM deliveries WHERE id=p_id FOR UPDATE;
 IF NOT FOUND THEN RAISE EXCEPTION 'Delivery not found' USING ERRCODE='P0002'; END IF;
 IF j.status<>'failed' THEN RAISE EXCEPTION 'Only failed deliveries can be retried' USING ERRCODE='22023'; END IF;
 UPDATE deliveries SET status='pending',cycle_attempts=0,last_error=NULL,next_attempt_at=now(),updated_at=now() WHERE id=p_id;
 INSERT INTO audit(order_id,action,detail) VALUES(j.order_id,'manual_retry',jsonb_build_object('delivery_id',p_id));
 RETURN jsonb_build_object('queued',true);
END $$;

CREATE FUNCTION api.set_destination_mode(p_mode text) RETURNS jsonb
LANGUAGE plpgsql SECURITY DEFINER SET search_path=internal,pg_temp AS $$
BEGIN
 IF p_mode NOT IN ('healthy','outage','lost_ack') OR p_mode IS NULL THEN RAISE EXCEPTION 'Unknown simulator mode' USING ERRCODE='22023'; END IF;
 UPDATE settings SET destination_mode=p_mode,fail_once=(p_mode='lost_ack') WHERE id=1;
 INSERT INTO audit(action,detail) VALUES('simulator_mode_changed',jsonb_build_object('mode',p_mode));
 RETURN jsonb_build_object('mode',p_mode);
END $$;

CREATE FUNCTION api.last_demo() RETURNS jsonb LANGUAGE sql STABLE SECURITY DEFINER SET search_path=internal,pg_temp AS $$ SELECT last_demo_event FROM settings WHERE id=1; $$;

CREATE FUNCTION api.accept_delivery(p_key uuid,p_payload jsonb) RETURNS jsonb
LANGUAGE plpgsql SECURITY DEFINER SET search_path=internal,pg_temp AS $$
DECLARE receipt destination_receipts; cfg settings; current_order destination_orders; fingerprint text; stale boolean:=false; source_name text; external_name text; version_num bigint;
BEGIN
 source_name:=p_payload->>'source';external_name:=p_payload->>'external_id';version_num:=(p_payload->>'version')::bigint;
 IF source_name NOT IN ('demo','stripe') OR external_name IS NULL OR version_num<1 THEN RAISE EXCEPTION 'Invalid delivery envelope' USING ERRCODE='22023'; END IF;
 fingerprint:=encode(sha256(convert_to(p_payload::text,'UTF8')),'hex');
 SELECT * INTO cfg FROM settings WHERE id=1 FOR UPDATE;
 IF cfg.destination_mode='outage' THEN RETURN jsonb_build_object('http_status',503,'error','Simulated fulfillment outage'); END IF;
 PERFORM pg_advisory_xact_lock(hashtextextended('receipt:'||p_key::text,0));
 SELECT * INTO receipt FROM destination_receipts WHERE idempotency_key=p_key;
 IF FOUND THEN
  IF receipt.payload_hash<>fingerprint THEN RAISE EXCEPTION 'Idempotency key reused with different payload' USING ERRCODE='23505'; END IF;
  RETURN jsonb_build_object('http_status',200,'duplicate',true,'ignored_stale',receipt.ignored_stale,'idempotency_key',p_key);
 END IF;
 PERFORM pg_advisory_xact_lock(hashtextextended('destination:'||source_name||':'||external_name,0));
 SELECT * INTO current_order FROM destination_orders WHERE source=source_name AND external_id=external_name FOR UPDATE;
 IF FOUND THEN
  IF version_num<current_order.version THEN stale:=true;
  ELSIF version_num=current_order.version THEN
   IF p_payload<>current_order.payload THEN RAISE EXCEPTION 'Destination version conflict' USING ERRCODE='23505'; END IF;
  ELSE
   UPDATE destination_orders SET version=version_num,payload=p_payload,updated_at=now() WHERE id=current_order.id;
  END IF;
 ELSE
  INSERT INTO destination_orders(source,external_id,version,payload) VALUES(source_name,external_name,version_num,p_payload);
 END IF;
 INSERT INTO destination_receipts(idempotency_key,payload_hash,source,external_id,version,ignored_stale) VALUES(p_key,fingerprint,source_name,external_name,version_num,stale);
 INSERT INTO audit(order_id,action,detail) SELECT id,'destination_committed',jsonb_build_object('idempotency_key',p_key,'version',version_num,'ignored_stale',stale) FROM orders WHERE source=source_name AND external_id=external_name;
 -- Simulate a commit whose acknowledgement is lost. The next attempt uses the
 -- same key and receives the existing receipt, rather than writing again.
 IF cfg.destination_mode='lost_ack' AND cfg.fail_once THEN
  UPDATE settings SET fail_once=false WHERE id=1;
  RETURN jsonb_build_object('http_status',503,'error','Simulated acknowledgement lost after commit');
 END IF;
 RETURN jsonb_build_object('http_status',200,'duplicate',false,'ignored_stale',stale,'idempotency_key',p_key);
END $$;

CREATE FUNCTION api.dashboard() RETURNS jsonb LANGUAGE sql STABLE SECURITY DEFINER SET search_path=internal,pg_temp AS $$
SELECT jsonb_build_object(
 'orders',(SELECT count(*) FROM orders),
 'delivered',(SELECT count(*) FROM orders o JOIN deliveries d ON d.id=o.latest_delivery WHERE d.status='succeeded'),
 'attention',(SELECT count(*) FROM orders o JOIN deliveries d ON d.id=o.latest_delivery WHERE d.status IN ('failed','retry_wait')),
 'duplicates',(SELECT coalesce(sum(duplicate_count),0)+count(*) FILTER(WHERE status='duplicate') FROM events),
 'queued',(SELECT count(*) FROM deliveries WHERE status IN ('pending','retry_wait','processing')),
 'destination_orders',(SELECT count(*) FROM destination_orders),
 'destination_receipts',(SELECT count(*) FROM destination_receipts),
 'mode',s.destination_mode,'worker_seen',s.worker_seen,
 'worker_ready',coalesce(s.worker_seen>now()-interval '30 seconds',false),
 'has_demo',s.last_demo_event IS NOT NULL
) FROM settings s WHERE id=1;
$$;
CREATE FUNCTION api.list_orders(p_search text DEFAULT '',p_status text DEFAULT '',p_offset integer DEFAULT 0) RETURNS jsonb LANGUAGE sql STABLE SECURITY DEFINER SET search_path=internal,pg_temp AS $$
WITH filtered AS (
 SELECT o.*,d.status,d.attempt_count,d.last_error FROM orders o JOIN deliveries d ON d.id=o.latest_delivery
 WHERE (p_search='' OR o.external_id ILIKE '%'||left(p_search,100)||'%' OR o.payload->>'customer' ILIKE '%'||left(p_search,100)||'%') AND (p_status='' OR d.status=p_status)
), page AS (SELECT * FROM filtered ORDER BY updated_at DESC,id LIMIT 25 OFFSET greatest(0,least(p_offset,100000)))
SELECT jsonb_build_object('total',(SELECT count(*) FROM filtered),'orders',coalesce((SELECT jsonb_agg(to_jsonb(page) ORDER BY updated_at DESC,id) FROM page),'[]'::jsonb));
$$;
CREATE FUNCTION api.order_details(p_id uuid) RETURNS jsonb LANGUAGE plpgsql STABLE SECURITY DEFINER SET search_path=internal,pg_temp AS $$
DECLARE o orders;
BEGIN
 SELECT * INTO o FROM orders WHERE id=p_id;
 IF NOT FOUND THEN RAISE EXCEPTION 'Order not found' USING ERRCODE='P0002'; END IF;
 RETURN jsonb_build_object('order',to_jsonb(o),
 'events',(SELECT coalesce(jsonb_agg(to_jsonb(e) ORDER BY e.created_at),'[]'::jsonb) FROM events e WHERE order_id=p_id),
 'deliveries',(SELECT coalesce(jsonb_agg(to_jsonb(d) ORDER BY version DESC),'[]'::jsonb) FROM deliveries d WHERE order_id=p_id),
 'attempts',(SELECT coalesce(jsonb_agg(to_jsonb(a) ORDER BY a.id DESC),'[]'::jsonb) FROM attempts a JOIN deliveries d ON d.id=a.delivery_id WHERE d.order_id=p_id),
 'audit',(SELECT coalesce(jsonb_agg(to_jsonb(recent) ORDER BY id DESC),'[]'::jsonb) FROM (SELECT * FROM audit WHERE order_id=p_id ORDER BY id DESC LIMIT 100) recent),
 'destination',(SELECT to_jsonb(dest) FROM destination_orders dest WHERE source=o.source AND external_id=o.external_id));
END $$;
CREATE FUNCTION api.delivery_log() RETURNS jsonb LANGUAGE sql STABLE SECURITY DEFINER SET search_path=internal,pg_temp AS $$
SELECT coalesce(jsonb_agg(to_jsonb(rows) ORDER BY created_at DESC),'[]'::jsonb) FROM
 (SELECT d.id,d.order_id,d.version,d.status,d.attempt_count,d.last_error,d.next_attempt_at,d.created_at,o.source,o.external_id FROM deliveries d JOIN orders o ON o.id=d.order_id ORDER BY d.created_at DESC LIMIT 100) rows;
$$;
CREATE FUNCTION api.activity() RETURNS jsonb LANGUAGE sql STABLE SECURITY DEFINER SET search_path=internal,pg_temp AS $$
SELECT coalesce(jsonb_agg(to_jsonb(rows) ORDER BY id DESC),'[]'::jsonb) FROM
 (SELECT a.*,o.external_id FROM audit a LEFT JOIN orders o ON o.id=a.order_id ORDER BY a.id DESC LIMIT 100) rows;
$$;
REVOKE ALL ON ALL FUNCTIONS IN SCHEMA api FROM PUBLIC;
GRANT USAGE ON SCHEMA api TO relay_app;
GRANT EXECUTE ON ALL FUNCTIONS IN SCHEMA api TO relay_app;
NOTIFY pgrst,'reload schema';
