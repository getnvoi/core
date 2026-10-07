--
-- PostgreSQL database dump
--


-- Dumped from database version 18.6 (Homebrew)
-- Dumped by pg_dump version 18.6 (Homebrew)

SET statement_timeout = 0;
SET lock_timeout = 0;
SET idle_in_transaction_session_timeout = 0;
SET transaction_timeout = 0;
SET client_encoding = 'UTF8';
SET standard_conforming_strings = on;
SELECT pg_catalog.set_config('search_path', '', false);
SET check_function_bodies = false;
SET xmloption = content;
SET client_min_messages = warning;
SET row_security = off;

--
-- Name: unaccent; Type: EXTENSION; Schema: -; Owner: -
--

CREATE EXTENSION IF NOT EXISTS unaccent WITH SCHEMA public;


--
-- Name: EXTENSION unaccent; Type: COMMENT; Schema: -; Owner: -
--

COMMENT ON EXTENSION unaccent IS 'text search dictionary that removes accents';


--
-- Name: oban_job_state; Type: TYPE; Schema: public; Owner: -
--

CREATE TYPE public.oban_job_state AS ENUM (
    'available',
    'suspended',
    'scheduled',
    'executing',
    'retryable',
    'completed',
    'discarded',
    'cancelled'
);


--
-- Name: artifacts_noticed(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.artifacts_noticed() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE turn uuid;
BEGIN
  FOR turn IN SELECT DISTINCT turn_id FROM public.artifact_versions
              WHERE artifact_id = NEW.id AND turn_id IS NOT NULL LOOP
    PERFORM public.turn_notice(turn);
  END LOOP;
  RETURN NULL;
END $$;


--
-- Name: attachments_noticed(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.attachments_noticed() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE r record; turn uuid;
BEGIN
  IF TG_OP = 'DELETE' THEN r := OLD; ELSE r := NEW; END IF;
  turn := CASE r.record_type
    WHEN 'Turn' THEN r.record_id
    WHEN 'Event' THEN (SELECT turn_id FROM public.events WHERE id = r.record_id)
    WHEN 'Capture' THEN (SELECT turn_id FROM public.captures WHERE id = r.record_id)
  END;
  IF turn IS NOT NULL THEN PERFORM public.turn_notice(turn); END IF;
  RETURN NULL;
END $$;


--
-- Name: deployments_production(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.deployments_production() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
  IF (SELECT kind FROM environments WHERE id = NEW.environment_id) IS DISTINCT FROM 'production' THEN
    RAISE EXCEPTION 'deployments are for production environments only';
  END IF;
  RETURN NEW;
END
$$;


--
-- Name: environments_deployed(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.environments_deployed() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
  IF OLD.kind = 'production' AND NEW.kind <> 'production'
     AND EXISTS (SELECT 1 FROM deployments WHERE environment_id = NEW.id) THEN
    RAISE EXCEPTION 'an environment with deployments stays production';
  END IF;
  RETURN NEW;
END
$$;


--
-- Name: images_production(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.images_production() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
  IF (SELECT kind FROM environments WHERE id = NEW.environment_id) IS DISTINCT FROM 'production' THEN
    RAISE EXCEPTION 'images are built for production environments only';
  END IF;
  RETURN NEW;
END
$$;


--
-- Name: thumbnails_noticed(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.thumbnails_noticed() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE turn uuid;
BEGIN
  FOR turn IN SELECT DISTINCT a.record_id FROM public.attachments a
              JOIN public.blobs b ON b.id = a.blob_id
              WHERE a.record_type = 'Turn' AND b.metadata LIKE '%' || NEW.digest || '%' LOOP
    PERFORM public.turn_notice(turn);
  END LOOP;
  RETURN NULL;
END $$;


--
-- Name: turn_notice(uuid); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.turn_notice(turn uuid) RETURNS void
    LANGUAGE plpgsql
    AS $$
DECLARE t record;
BEGIN
  SELECT id, agent_session_id, lead_id INTO t FROM public.turns WHERE id = turn;
  IF FOUND THEN
    PERFORM pg_notify('turn', t.agent_session_id::text || ':' || t.id::text);
    IF t.lead_id IS NOT NULL THEN
      PERFORM pg_notify('turn', t.agent_session_id::text || ':' || t.lead_id::text);
    END IF;
  END IF;
END $$;


--
-- Name: turn_rows_noticed(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.turn_rows_noticed() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE r record;
BEGIN
  IF TG_OP = 'DELETE' THEN r := OLD; ELSE r := NEW; END IF;
  IF r.turn_id IS NOT NULL THEN PERFORM public.turn_notice(r.turn_id); END IF;
  RETURN NULL;
END $$;


--
-- Name: turns_noticed(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.turns_noticed() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE r record;
BEGIN
  IF TG_OP = 'DELETE' THEN r := OLD; ELSE r := NEW; END IF;
  PERFORM pg_notify('turn', r.agent_session_id::text || ':' || r.id::text);
  IF r.lead_id IS NOT NULL THEN
    PERFORM pg_notify('turn', r.agent_session_id::text || ':' || r.lead_id::text);
  END IF;
  RETURN NULL;
END $$;


--
-- Name: unaccented(text); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.unaccented(text) RETURNS text
    LANGUAGE sql IMMUTABLE STRICT PARALLEL SAFE
    RETURN public.unaccent('public.unaccent'::regdictionary, $1);


SET default_tablespace = '';

SET default_table_access_method = heap;

--
-- Name: activities; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.activities (
    id uuid DEFAULT uuidv7() NOT NULL,
    workspace_id uuid NOT NULL,
    user_id uuid,
    record_type character varying(255),
    record_id uuid,
    event_name character varying(255) NOT NULL,
    level character varying(255) DEFAULT 'info'::character varying NOT NULL,
    source character varying(255) NOT NULL,
    properties jsonb DEFAULT '{}'::jsonb NOT NULL,
    duration_ms integer,
    path character varying(255),
    created_at timestamp without time zone NOT NULL
);


--
-- Name: agent_session_states; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.agent_session_states (
    id uuid DEFAULT uuidv7() NOT NULL,
    created_at timestamp(6) without time zone NOT NULL,
    name character varying NOT NULL,
    "position" integer NOT NULL,
    updated_at timestamp(6) without time zone NOT NULL,
    workspace_id uuid NOT NULL
);


--
-- Name: agent_sessions; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.agent_sessions (
    id uuid DEFAULT uuidv7() NOT NULL,
    agent_id uuid,
    archived_at timestamp(6) without time zone,
    created_at timestamp(6) without time zone NOT NULL,
    digest character varying,
    gate_output text,
    hidden boolean DEFAULT false NOT NULL,
    llm_config_id uuid,
    parent_id uuid,
    sdk_session_id character varying,
    step_state character varying,
    subject character varying,
    task_id uuid,
    title character varying,
    updated_at timestamp(6) without time zone NOT NULL,
    user_id uuid,
    workflow_id uuid,
    workflow_step_id uuid,
    workspace_id uuid,
    worktree_id uuid NOT NULL,
    agent_session_state_id uuid,
    terms tsvector GENERATED ALWAYS AS (to_tsvector('simple'::regconfig, public.unaccented((COALESCE(title, ''::character varying))::text))) STORED
);


--
-- Name: agent_skills; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.agent_skills (
    id uuid DEFAULT uuidv7() NOT NULL,
    agent_id uuid NOT NULL,
    created_at timestamp(6) without time zone NOT NULL,
    skill_id uuid NOT NULL,
    updated_at timestamp(6) without time zone NOT NULL
);


--
-- Name: agents; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.agents (
    id uuid DEFAULT uuidv7() NOT NULL,
    archived_at timestamp(6) without time zone,
    created_at timestamp(6) without time zone NOT NULL,
    instructions text DEFAULT ''::text NOT NULL,
    model character varying,
    name character varying NOT NULL,
    slug character varying NOT NULL,
    tagline character varying,
    tools jsonb DEFAULT '{}'::jsonb NOT NULL,
    updated_at timestamp(6) without time zone NOT NULL,
    workspace_id uuid NOT NULL,
    llm_config_id uuid
);


--
-- Name: artifact_versions; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.artifact_versions (
    id uuid DEFAULT uuidv7() NOT NULL,
    artifact_id uuid NOT NULL,
    created_at timestamp(6) without time zone NOT NULL,
    digest character varying NOT NULL,
    message text,
    number integer NOT NULL,
    sha character varying,
    turn_id uuid,
    updated_at timestamp(6) without time zone NOT NULL,
    terms tsvector GENERATED ALWAYS AS (to_tsvector('simple'::regconfig, public.unaccented(COALESCE(message, ''::text)))) STORED,
    content text,
    user_id uuid,
    exports jsonb DEFAULT '{}'::jsonb NOT NULL
);


--
-- Name: artifacts; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.artifacts (
    id uuid DEFAULT uuidv7() NOT NULL,
    archived_at timestamp(6) without time zone,
    created_at timestamp(6) without time zone NOT NULL,
    name character varying NOT NULL,
    port integer,
    public boolean DEFAULT false NOT NULL,
    served_at timestamp(6) without time zone,
    served_number integer,
    session_id uuid,
    shared boolean DEFAULT false NOT NULL,
    slug character varying NOT NULL,
    state character varying DEFAULT 'new'::character varying NOT NULL,
    updated_at timestamp(6) without time zone NOT NULL,
    used_at timestamp(6) without time zone,
    viewer_key text,
    workspace_id uuid NOT NULL,
    worktree_id uuid,
    terms tsvector GENERATED ALWAYS AS (to_tsvector('simple'::regconfig, public.unaccented((name)::text))) STORED,
    kind character varying DEFAULT 'app'::character varying NOT NULL
);


--
-- Name: attachments; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.attachments (
    id uuid DEFAULT uuidv7() NOT NULL,
    blob_id uuid NOT NULL,
    created_at timestamp(6) without time zone NOT NULL,
    name character varying NOT NULL,
    record_id uuid NOT NULL,
    record_type character varying NOT NULL
);


--
-- Name: blobs; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.blobs (
    id uuid DEFAULT uuidv7() NOT NULL,
    byte_size bigint NOT NULL,
    checksum character varying,
    content_type character varying,
    created_at timestamp(6) without time zone NOT NULL,
    filename character varying NOT NULL,
    key character varying NOT NULL,
    metadata text,
    service_name character varying NOT NULL
);


--
-- Name: builders; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.builders (
    id uuid DEFAULT uuidv7() NOT NULL,
    environment_id uuid NOT NULL,
    name character varying(255) NOT NULL,
    provider_name character varying(255) NOT NULL,
    box_id character varying(255),
    used_at timestamp without time zone,
    created_at timestamp without time zone NOT NULL,
    updated_at timestamp without time zone NOT NULL
);


--
-- Name: captures; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.captures (
    id uuid DEFAULT uuidv7() NOT NULL,
    created_at timestamp(6) without time zone NOT NULL,
    description text,
    label character varying NOT NULL,
    turn_id uuid NOT NULL,
    updated_at timestamp(6) without time zone NOT NULL,
    url character varying NOT NULL,
    kind character varying DEFAULT 'screenshot'::character varying NOT NULL,
    digest character varying
);


--
-- Name: cloud_credentials; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.cloud_credentials (
    id uuid DEFAULT uuidv7() NOT NULL,
    workspace_id uuid NOT NULL,
    provider character varying(255) NOT NULL,
    token text NOT NULL,
    checked_at timestamp without time zone,
    created_at timestamp without time zone NOT NULL,
    updated_at timestamp without time zone NOT NULL
);


--
-- Name: clusters; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.clusters (
    id uuid DEFAULT uuidv7() NOT NULL,
    environment_id uuid NOT NULL,
    name character varying(255) NOT NULL,
    provider character varying(255) NOT NULL,
    location character varying(255) NOT NULL,
    server_type character varying(255) NOT NULL,
    token text NOT NULL,
    private_key text NOT NULL,
    public_key text NOT NULL,
    status character varying(255) DEFAULT 'creating'::character varying NOT NULL,
    error text,
    address character varying(255),
    created_at timestamp without time zone NOT NULL,
    updated_at timestamp without time zone NOT NULL,
    CONSTRAINT clusters_status CHECK (((status)::text = ANY ((ARRAY['creating'::character varying, 'ready'::character varying, 'failed'::character varying, 'destroying'::character varying])::text[])))
);


--
-- Name: connectors; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.connectors (
    id uuid DEFAULT uuidv7() NOT NULL,
    account_login character varying,
    account_type character varying,
    created_at timestamp(6) without time zone NOT NULL,
    github_installation_id bigint,
    installed_at timestamp(6) without time zone,
    installer_id uuid,
    kind character varying DEFAULT 'token'::character varying,
    repos_sync_status character varying DEFAULT 'idle'::character varying NOT NULL,
    repos_synced_at timestamp(6) without time zone,
    source character varying NOT NULL,
    suspended_at timestamp(6) without time zone,
    token text,
    updated_at timestamp(6) without time zone NOT NULL,
    workspace_id uuid,
    archived_at timestamp without time zone
);


--
-- Name: databases; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.databases (
    id uuid DEFAULT uuidv7() NOT NULL,
    archived_at timestamp(6) without time zone,
    created_at timestamp(6) without time zone NOT NULL,
    engine character varying NOT NULL,
    environment_id uuid NOT NULL,
    limits jsonb DEFAULT '{}'::jsonb NOT NULL,
    parent_id uuid,
    provider_name character varying NOT NULL,
    ref character varying,
    secrets text,
    updated_at timestamp(6) without time zone NOT NULL,
    version character varying NOT NULL,
    worktree_id uuid,
    error text
);


--
-- Name: deployments; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.deployments (
    id uuid DEFAULT uuidv7() NOT NULL,
    environment_id uuid NOT NULL,
    commit character varying(255) NOT NULL,
    status character varying(255) NOT NULL,
    log text DEFAULT ''::text NOT NULL,
    error text,
    variables text,
    variables_version character varying(255),
    started_by_id uuid,
    allowed_by_id uuid,
    rollback_of_id uuid,
    finished_at timestamp without time zone,
    created_at timestamp without time zone NOT NULL,
    updated_at timestamp without time zone NOT NULL,
    image_id uuid,
    CONSTRAINT deployments_status CHECK (((status)::text = ANY ((ARRAY['waiting'::character varying, 'queued'::character varying, 'building'::character varying, 'deploying'::character varying, 'live'::character varying, 'failed'::character varying, 'rolled_back'::character varying, 'denied'::character varying])::text[])))
);


--
-- Name: ecto_migrations; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.ecto_migrations (
    version bigint NOT NULL,
    inserted_at timestamp(0) without time zone
);


--
-- Name: env_vars; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.env_vars (
    id uuid DEFAULT uuidv7() NOT NULL,
    created_at timestamp(6) without time zone NOT NULL,
    environment_id uuid NOT NULL,
    key character varying NOT NULL,
    secret boolean DEFAULT false NOT NULL,
    updated_at timestamp(6) without time zone NOT NULL,
    value text NOT NULL,
    archived_at timestamp without time zone
);


--
-- Name: environment_commands; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.environment_commands (
    id uuid DEFAULT uuidv7() NOT NULL,
    before character varying[] DEFAULT '{}'::character varying[] NOT NULL,
    command character varying NOT NULL,
    created_at timestamp(6) without time zone NOT NULL,
    environment_id uuid NOT NULL,
    kind character varying DEFAULT 'default'::character varying NOT NULL,
    name character varying NOT NULL,
    port integer,
    "position" integer DEFAULT 0 NOT NULL,
    updated_at timestamp(6) without time zone NOT NULL,
    archived_at timestamp without time zone
);


--
-- Name: environments; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.environments (
    id uuid DEFAULT uuidv7() NOT NULL,
    branch character varying NOT NULL,
    created_at timestamp(6) without time zone NOT NULL,
    database_engine character varying,
    database_version character varying,
    disk integer NOT NULL,
    manifest character varying,
    name character varying NOT NULL,
    preview_port integer,
    provider_name character varying NOT NULL,
    repo_id uuid NOT NULL,
    shape character varying DEFAULT '2x4'::character varying NOT NULL,
    updated_at timestamp(6) without time zone NOT NULL,
    restricted boolean DEFAULT false NOT NULL,
    archived_at timestamp without time zone,
    kind character varying(255) DEFAULT 'development'::character varying NOT NULL,
    deploy character varying(255) DEFAULT 'approval'::character varying NOT NULL,
    images_kept integer DEFAULT 10 NOT NULL,
    CONSTRAINT environments_deploy CHECK (((deploy)::text = ANY ((ARRAY['auto'::character varying, 'approval'::character varying])::text[]))),
    CONSTRAINT environments_images_kept CHECK ((images_kept >= 1)),
    CONSTRAINT environments_kind CHECK (((kind)::text = ANY ((ARRAY['development'::character varying, 'production'::character varying])::text[])))
);


--
-- Name: events; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.events (
    id uuid DEFAULT uuidv7() NOT NULL,
    approval_status character varying,
    arguments_digest character varying,
    content text,
    created_at timestamp(6) without time zone NOT NULL,
    decided_at timestamp(6) without time zone,
    duration_ms integer,
    handed_at timestamp(6) without time zone,
    kind character varying NOT NULL,
    outcome text,
    parent_id uuid,
    parent_tool_use_id character varying,
    tool character varying,
    tool_use_id character varying,
    turn_id uuid,
    updated_at timestamp(6) without time zone NOT NULL,
    user_id uuid,
    agent_session_id uuid,
    arguments text,
    picked_at timestamp without time zone
);


--
-- Name: funnels; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.funnels (
    id uuid DEFAULT uuidv7() NOT NULL,
    key character varying(255),
    "group" character varying(255) NOT NULL,
    name character varying(255) NOT NULL,
    description text DEFAULT ''::text NOT NULL,
    kind character varying(255) DEFAULT 'sequence'::character varying NOT NULL,
    actor character varying(255) DEFAULT 'user'::character varying NOT NULL,
    window_days integer DEFAULT 30 NOT NULL,
    steps jsonb DEFAULT '[]'::jsonb NOT NULL,
    "position" integer DEFAULT 0 NOT NULL,
    created_at timestamp without time zone NOT NULL,
    updated_at timestamp without time zone NOT NULL
);


--
-- Name: github_install_intents; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.github_install_intents (
    id uuid DEFAULT uuidv7() NOT NULL,
    created_at timestamp(6) without time zone NOT NULL,
    message character varying,
    outcome character varying,
    settled_at timestamp(6) without time zone,
    updated_at timestamp(6) without time zone NOT NULL,
    user_id uuid NOT NULL,
    "window" boolean DEFAULT false NOT NULL,
    workspace_id uuid NOT NULL,
    nonce_digest bytea
);


--
-- Name: github_repositories; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.github_repositories (
    id uuid DEFAULT uuidv7() NOT NULL,
    created_at timestamp(6) without time zone NOT NULL,
    full_name character varying NOT NULL,
    github_id bigint NOT NULL,
    github_installation_id uuid NOT NULL,
    private boolean NOT NULL,
    pushed_at timestamp(6) without time zone,
    updated_at timestamp(6) without time zone NOT NULL
);


--
-- Name: github_sign_ins; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.github_sign_ins (
    id uuid DEFAULT uuidv7() NOT NULL,
    key_digest bytea NOT NULL,
    nonce_digest bytea,
    verifier text NOT NULL,
    state character varying(255) DEFAULT 'pending'::character varying NOT NULL,
    message character varying(255),
    user_id uuid,
    workspace_id uuid,
    settled_at timestamp without time zone,
    collected_at timestamp without time zone,
    created_at timestamp without time zone NOT NULL,
    updated_at timestamp without time zone NOT NULL,
    code_digest bytea,
    code_misses integer DEFAULT 0 NOT NULL
);


--
-- Name: grants; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.grants (
    id uuid DEFAULT uuidv7() NOT NULL,
    workspace_user_id uuid NOT NULL,
    environment_id uuid NOT NULL,
    created_at timestamp(6) without time zone NOT NULL,
    updated_at timestamp(6) without time zone NOT NULL
);


--
-- Name: images; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.images (
    id uuid DEFAULT uuidv7() NOT NULL,
    environment_id uuid NOT NULL,
    builder_id uuid,
    commit character varying(255) NOT NULL,
    status character varying(255) DEFAULT 'queued'::character varying NOT NULL,
    digest character varying(255),
    size bigint,
    storage_key character varying(255),
    error text,
    user_id uuid,
    agent_session_id uuid,
    finished_at timestamp without time zone,
    created_at timestamp without time zone NOT NULL,
    updated_at timestamp without time zone NOT NULL,
    CONSTRAINT images_status CHECK (((status)::text = ANY ((ARRAY['queued'::character varying, 'building'::character varying, 'ready'::character varying, 'failed'::character varying, 'pruned'::character varying])::text[])))
);


--
-- Name: library_items; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.library_items (
    id uuid DEFAULT uuidv7() NOT NULL,
    archived_at timestamp(6) without time zone,
    byte_size bigint DEFAULT 0 NOT NULL,
    content_type character varying DEFAULT 'application/octet-stream'::character varying NOT NULL,
    created_at timestamp(6) without time zone NOT NULL,
    digest character varying,
    event_id uuid,
    name character varying NOT NULL,
    updated_at timestamp(6) without time zone NOT NULL,
    workspace_id uuid NOT NULL,
    body text,
    terms tsvector GENERATED ALWAYS AS (to_tsvector('simple'::regconfig, public.unaccented((((name)::text || ' '::text) || "left"(COALESCE(body, ''::text), 100000))))) STORED,
    read_at timestamp(6) without time zone
);


--
-- Name: llm_configs; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.llm_configs (
    id uuid DEFAULT uuidv7() NOT NULL,
    archived_at timestamp(6) without time zone,
    created_at timestamp(6) without time zone NOT NULL,
    label character varying NOT NULL,
    main boolean DEFAULT false NOT NULL,
    provider character varying NOT NULL,
    secrets text,
    settings jsonb DEFAULT '{}'::jsonb NOT NULL,
    updated_at timestamp(6) without time zone NOT NULL,
    workspace_id uuid NOT NULL
);


--
-- Name: llm_connections; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.llm_connections (
    id uuid DEFAULT uuidv7() NOT NULL,
    workspace_id uuid NOT NULL,
    user_id uuid NOT NULL,
    llm_config_id uuid,
    provider character varying NOT NULL,
    label character varying NOT NULL,
    state character varying DEFAULT 'pending'::character varying NOT NULL,
    reason text,
    device_auth_id text,
    user_code text,
    verification_url character varying,
    "interval" integer DEFAULT 5 NOT NULL,
    failures integer DEFAULT 0 NOT NULL,
    expires_at timestamp(6) without time zone NOT NULL,
    created_at timestamp(6) without time zone NOT NULL,
    updated_at timestamp(6) without time zone NOT NULL
);


--
-- Name: logs; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.logs (
    id uuid DEFAULT uuidv7() NOT NULL,
    created_at timestamp(6) without time zone NOT NULL,
    record_id uuid NOT NULL,
    record_type character varying NOT NULL,
    output text,
    step character varying NOT NULL,
    updated_at timestamp(6) without time zone NOT NULL
);


--
-- Name: note_mentions; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.note_mentions (
    id uuid DEFAULT uuidv7() NOT NULL,
    created_at timestamp(6) without time zone NOT NULL,
    event_id uuid NOT NULL,
    handled_at timestamp(6) without time zone,
    updated_at timestamp(6) without time zone NOT NULL,
    user_id uuid NOT NULL
);


--
-- Name: oauth_clients; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.oauth_clients (
    id uuid DEFAULT uuidv7() NOT NULL,
    name character varying(255) NOT NULL,
    redirect_uris text[] NOT NULL,
    created_at timestamp without time zone NOT NULL,
    updated_at timestamp without time zone NOT NULL
);


--
-- Name: oauth_codes; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.oauth_codes (
    id uuid DEFAULT uuidv7() NOT NULL,
    code_digest bytea NOT NULL,
    oauth_client_id uuid NOT NULL,
    user_id uuid NOT NULL,
    workspace_id uuid NOT NULL,
    redirect_uri text NOT NULL,
    challenge character varying(255) NOT NULL,
    expires_at timestamp without time zone NOT NULL,
    created_at timestamp without time zone NOT NULL,
    updated_at timestamp without time zone NOT NULL
);


--
-- Name: oban_jobs; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.oban_jobs (
    id bigint NOT NULL,
    state public.oban_job_state DEFAULT 'available'::public.oban_job_state NOT NULL,
    queue text DEFAULT 'default'::text NOT NULL,
    worker text NOT NULL,
    args jsonb DEFAULT '{}'::jsonb NOT NULL,
    errors jsonb[] DEFAULT ARRAY[]::jsonb[] NOT NULL,
    attempt integer DEFAULT 0 NOT NULL,
    max_attempts integer DEFAULT 20 NOT NULL,
    inserted_at timestamp without time zone DEFAULT timezone('UTC'::text, now()) NOT NULL,
    scheduled_at timestamp without time zone DEFAULT timezone('UTC'::text, now()) NOT NULL,
    attempted_at timestamp without time zone,
    completed_at timestamp without time zone,
    attempted_by text[],
    discarded_at timestamp without time zone,
    priority integer DEFAULT 0 NOT NULL,
    tags text[] DEFAULT ARRAY[]::text[],
    meta jsonb DEFAULT '{}'::jsonb,
    cancelled_at timestamp without time zone,
    CONSTRAINT attempt_range CHECK (((attempt >= 0) AND (attempt <= max_attempts))),
    CONSTRAINT positive_max_attempts CHECK ((max_attempts > 0)),
    CONSTRAINT queue_length CHECK (((char_length(queue) > 0) AND (char_length(queue) < 128))),
    CONSTRAINT worker_length CHECK (((char_length(worker) > 0) AND (char_length(worker) < 128)))
);


--
-- Name: TABLE oban_jobs; Type: COMMENT; Schema: public; Owner: -
--

COMMENT ON TABLE public.oban_jobs IS '14';


--
-- Name: oban_jobs_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.oban_jobs_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: oban_jobs_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.oban_jobs_id_seq OWNED BY public.oban_jobs.id;


--
-- Name: oban_peers; Type: TABLE; Schema: public; Owner: -
--

CREATE UNLOGGED TABLE public.oban_peers (
    name text NOT NULL,
    node text NOT NULL,
    started_at timestamp without time zone NOT NULL,
    expires_at timestamp without time zone NOT NULL
);


--
-- Name: prices; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.prices (
    id uuid DEFAULT uuidv7() NOT NULL,
    cents_per_hour numeric NOT NULL,
    created_at timestamp(6) without time zone NOT NULL,
    fetched_at timestamp(6) without time zone NOT NULL,
    key character varying NOT NULL,
    provider_name character varying NOT NULL,
    source character varying NOT NULL,
    updated_at timestamp(6) without time zone NOT NULL
);


--
-- Name: provisionings; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.provisionings (
    id uuid DEFAULT uuidv7() NOT NULL,
    created_at timestamp(6) without time zone NOT NULL,
    kind character varying DEFAULT 'box'::character varying NOT NULL,
    status character varying DEFAULT 'pending'::character varying NOT NULL,
    updated_at timestamp(6) without time zone NOT NULL,
    worktree_id uuid NOT NULL
);


--
-- Name: repos; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.repos (
    id uuid DEFAULT uuidv7() NOT NULL,
    created_at timestamp(6) without time zone NOT NULL,
    default_branch character varying,
    github_installation_id uuid,
    host character varying DEFAULT 'github'::character varying NOT NULL,
    name character varying NOT NULL,
    updated_at timestamp(6) without time zone NOT NULL,
    workspace_id uuid NOT NULL,
    archived_at timestamp without time zone
);


--
-- Name: seeds; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.seeds (
    id uuid DEFAULT uuidv7() NOT NULL,
    box_id character varying,
    branch character varying,
    built_at timestamp(6) without time zone,
    commands_digest character varying,
    commit character varying,
    created_at timestamp(6) without time zone NOT NULL,
    environment_id uuid NOT NULL,
    generation integer DEFAULT 0 NOT NULL,
    host_key character varying,
    parked_at timestamp(6) without time zone,
    provider_name character varying NOT NULL,
    state character varying DEFAULT 'missing'::character varying NOT NULL,
    updated_at timestamp(6) without time zone NOT NULL,
    used_at timestamp(6) without time zone,
    name character varying(255)
);


--
-- Name: service_runs; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.service_runs (
    id uuid DEFAULT uuidv7() NOT NULL,
    environment_id uuid NOT NULL,
    user_id uuid,
    service character varying(255) NOT NULL,
    command text NOT NULL,
    variables text,
    variable_keys character varying(255)[] DEFAULT ARRAY[]::character varying[] NOT NULL,
    status character varying(255) DEFAULT 'queued'::character varying NOT NULL,
    mode character varying(255),
    pod character varying(255),
    exit_status integer,
    output text,
    error text,
    finished_at timestamp without time zone,
    created_at timestamp without time zone NOT NULL,
    updated_at timestamp without time zone NOT NULL
);


--
-- Name: services; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.services (
    id uuid DEFAULT uuidv7() NOT NULL,
    environment_id uuid NOT NULL,
    name character varying(255) NOT NULL,
    command text,
    image character varying(255),
    replicas integer DEFAULT 1 NOT NULL,
    strategy character varying(255) DEFAULT 'RollingUpdate'::character varying NOT NULL,
    port integer,
    cpu character varying(255),
    memory character varying(255),
    created_at timestamp without time zone NOT NULL,
    updated_at timestamp without time zone NOT NULL,
    CONSTRAINT services_replicas CHECK ((replicas >= 0)),
    CONSTRAINT services_strategy CHECK (((strategy)::text = ANY ((ARRAY['Recreate'::character varying, 'RollingUpdate'::character varying])::text[])))
);


--
-- Name: session_presences; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.session_presences (
    id uuid DEFAULT uuidv7() NOT NULL,
    connections integer DEFAULT 1 NOT NULL,
    created_at timestamp(6) without time zone NOT NULL,
    seen_at timestamp(6) without time zone NOT NULL,
    updated_at timestamp(6) without time zone NOT NULL,
    user_id uuid NOT NULL,
    agent_session_id uuid NOT NULL
);


--
-- Name: sessions; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.sessions (
    id uuid DEFAULT uuidv7() NOT NULL,
    created_at timestamp(6) without time zone NOT NULL,
    ip_address character varying,
    updated_at timestamp(6) without time zone NOT NULL,
    user_agent character varying,
    user_id uuid NOT NULL,
    workspace_id uuid,
    kind character varying(255) DEFAULT 'app'::character varying NOT NULL,
    name character varying(255),
    expires_at timestamp without time zone,
    last_used_at timestamp without time zone,
    refresh_digest bytea,
    oauth_client_id uuid
);


--
-- Name: skill_files; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.skill_files (
    id uuid DEFAULT uuidv7() NOT NULL,
    archived_at timestamp(6) without time zone,
    created_at timestamp(6) without time zone NOT NULL,
    path character varying NOT NULL,
    "position" integer DEFAULT 0 NOT NULL,
    skill_id uuid NOT NULL,
    updated_at timestamp(6) without time zone NOT NULL
);


--
-- Name: skills; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.skills (
    id uuid DEFAULT uuidv7() NOT NULL,
    archived_at timestamp(6) without time zone,
    body text DEFAULT ''::text NOT NULL,
    created_at timestamp(6) without time zone NOT NULL,
    description text,
    display_title character varying,
    example character varying,
    icon character varying,
    name character varying NOT NULL,
    slug character varying NOT NULL,
    source character varying DEFAULT 'custom'::character varying NOT NULL,
    tagline character varying,
    updated_at timestamp(6) without time zone NOT NULL,
    workspace_id uuid NOT NULL
);


--
-- Name: storage_costs; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.storage_costs (
    id uuid DEFAULT uuidv7() NOT NULL,
    workspace_id uuid NOT NULL,
    environment_id uuid,
    category character varying(255) NOT NULL,
    day date NOT NULL,
    bytes bigint NOT NULL,
    cents numeric NOT NULL,
    billed boolean DEFAULT false NOT NULL,
    created_at timestamp without time zone NOT NULL,
    updated_at timestamp without time zone NOT NULL
);


--
-- Name: system_users; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.system_users (
    user_id uuid NOT NULL,
    created_at timestamp without time zone NOT NULL
);


--
-- Name: tasks; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.tasks (
    id uuid DEFAULT uuidv7() NOT NULL,
    actions character varying[] DEFAULT '{}'::character varying[] NOT NULL,
    agent_id uuid NOT NULL,
    archived_at timestamp(6) without time zone,
    created_at timestamp(6) without time zone NOT NULL,
    cron character varying,
    environment_id uuid NOT NULL,
    event character varying,
    filters jsonb DEFAULT '{}'::jsonb NOT NULL,
    last_error text,
    last_run_at timestamp(6) without time zone,
    name character varying NOT NULL,
    next_run_at timestamp(6) without time zone,
    paused_at timestamp(6) without time zone,
    prompt text NOT NULL,
    source character varying,
    timezone character varying DEFAULT 'UTC'::character varying NOT NULL,
    token_digest character varying,
    trigger character varying DEFAULT 'cron'::character varying NOT NULL,
    updated_at timestamp(6) without time zone NOT NULL,
    workflow_id uuid,
    workspace_id uuid NOT NULL
);


--
-- Name: thumbnails; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.thumbnails (
    id uuid DEFAULT uuidv7() NOT NULL,
    workspace_id uuid NOT NULL,
    digest character varying NOT NULL,
    state character varying DEFAULT 'ready'::character varying NOT NULL,
    created_at timestamp(6) without time zone NOT NULL,
    updated_at timestamp(6) without time zone NOT NULL
);


--
-- Name: tool_grants; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.tool_grants (
    id uuid DEFAULT uuidv7() NOT NULL,
    workspace_user_id uuid NOT NULL,
    tool character varying NOT NULL,
    level character varying NOT NULL,
    overridden boolean DEFAULT false NOT NULL,
    created_at timestamp(6) without time zone NOT NULL,
    updated_at timestamp(6) without time zone NOT NULL
);


--
-- Name: tool_presets; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.tool_presets (
    id uuid DEFAULT uuidv7() NOT NULL,
    workspace_id uuid NOT NULL,
    role character varying NOT NULL,
    tool character varying NOT NULL,
    level character varying NOT NULL,
    created_at timestamp(6) without time zone NOT NULL,
    updated_at timestamp(6) without time zone NOT NULL
);


--
-- Name: turns; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.turns (
    id uuid DEFAULT uuidv7() NOT NULL,
    conversation character varying,
    created_at timestamp(6) without time zone NOT NULL,
    internal boolean DEFAULT false NOT NULL,
    lead_id uuid,
    log_offset bigint DEFAULT 0 NOT NULL,
    note_id uuid,
    prompt text NOT NULL,
    status character varying DEFAULT 'pending'::character varying NOT NULL,
    updated_at timestamp(6) without time zone NOT NULL,
    user_id uuid,
    agent_session_id uuid NOT NULL,
    terms tsvector GENERATED ALWAYS AS (to_tsvector('simple'::regconfig, public.unaccented("left"(prompt, 100000)))) STORED,
    input_tokens bigint DEFAULT 0 NOT NULL,
    output_tokens bigint DEFAULT 0 NOT NULL,
    cache_read_tokens bigint DEFAULT 0 NOT NULL,
    cache_write_tokens bigint DEFAULT 0 NOT NULL,
    cents numeric(14,6)
);


--
-- Name: users; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.users (
    id uuid DEFAULT uuidv7() NOT NULL,
    created_at timestamp(6) without time zone NOT NULL,
    email character varying NOT NULL,
    github_login character varying,
    github_user_id bigint,
    password_digest character varying NOT NULL,
    updated_at timestamp(6) without time zone NOT NULL,
    first_name character varying,
    last_name character varying,
    pending_approval boolean DEFAULT true NOT NULL
);


--
-- Name: variants; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.variants (
    id uuid DEFAULT uuidv7() NOT NULL,
    blob_id uuid NOT NULL,
    variation_digest character varying NOT NULL
);


--
-- Name: vm_costs; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.vm_costs (
    id uuid DEFAULT uuidv7() NOT NULL,
    billed_seconds integer DEFAULT 0 NOT NULL,
    box_id character varying NOT NULL,
    cents numeric DEFAULT 0.0 NOT NULL,
    created_at timestamp(6) without time zone NOT NULL,
    day date NOT NULL,
    disk integer NOT NULL,
    held_seconds integer DEFAULT 0 NOT NULL,
    name character varying NOT NULL,
    owner_id uuid,
    owner_type character varying,
    provider_name character varying NOT NULL,
    running_seconds integer DEFAULT 0 NOT NULL,
    shape character varying NOT NULL,
    source character varying NOT NULL,
    updated_at timestamp(6) without time zone NOT NULL,
    workspace_id uuid
);


--
-- Name: vm_snapshots; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.vm_snapshots (
    id uuid DEFAULT uuidv7() NOT NULL,
    box_id character varying NOT NULL,
    cents numeric,
    costed boolean DEFAULT false NOT NULL,
    created_at timestamp(6) without time zone NOT NULL,
    disk integer NOT NULL,
    name character varying NOT NULL,
    owner_id uuid,
    owner_type character varying,
    provider_name character varying NOT NULL,
    seconds integer,
    shape character varying NOT NULL,
    state character varying NOT NULL,
    taken_at timestamp(6) without time zone NOT NULL,
    workspace_id uuid
);


--
-- Name: webhooks; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.webhooks (
    id uuid DEFAULT uuidv7() NOT NULL,
    created_at timestamp(6) without time zone NOT NULL,
    delivery_id character varying,
    event character varying,
    headers jsonb DEFAULT '{}'::jsonb NOT NULL,
    outcomes jsonb DEFAULT '{}'::jsonb NOT NULL,
    payload jsonb DEFAULT '{}'::jsonb NOT NULL,
    processed_at timestamp(6) without time zone,
    received_at timestamp(6) without time zone NOT NULL,
    source character varying NOT NULL,
    updated_at timestamp(6) without time zone NOT NULL
);


--
-- Name: workflow_steps; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.workflow_steps (
    id uuid DEFAULT uuidv7() NOT NULL,
    advance character varying DEFAULT 'auto'::character varying NOT NULL,
    agent_id uuid NOT NULL,
    created_at timestamp(6) without time zone NOT NULL,
    gate_command character varying,
    instructions text NOT NULL,
    name character varying NOT NULL,
    "position" integer NOT NULL,
    updated_at timestamp(6) without time zone NOT NULL,
    workflow_id uuid NOT NULL,
    agent_session_state_id uuid
);


--
-- Name: workflows; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.workflows (
    id uuid DEFAULT uuidv7() NOT NULL,
    archived_at timestamp(6) without time zone,
    created_at timestamp(6) without time zone NOT NULL,
    environment_id uuid,
    name character varying NOT NULL,
    updated_at timestamp(6) without time zone NOT NULL,
    workspace_id uuid NOT NULL
);


--
-- Name: workspace_users; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.workspace_users (
    id uuid DEFAULT uuidv7() NOT NULL,
    created_at timestamp(6) without time zone NOT NULL,
    role character varying DEFAULT 'editor'::character varying NOT NULL,
    updated_at timestamp(6) without time zone NOT NULL,
    user_id uuid NOT NULL,
    workspace_id uuid NOT NULL,
    archived_at timestamp without time zone
);


--
-- Name: workspaces; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.workspaces (
    id uuid DEFAULT uuidv7() NOT NULL,
    created_at timestamp(6) without time zone NOT NULL,
    name character varying NOT NULL,
    onboarded_at timestamp(6) without time zone,
    slug character varying NOT NULL,
    updated_at timestamp(6) without time zone NOT NULL
);


--
-- Name: worktrees; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.worktrees (
    id uuid DEFAULT uuidv7() NOT NULL,
    address character varying,
    base_ref character varying NOT NULL,
    box_id character varying,
    branch character varying NOT NULL,
    created_at timestamp(6) without time zone NOT NULL,
    environment_id uuid NOT NULL,
    host_key character varying,
    hostname character varying,
    patch text,
    patch_at timestamp(6) without time zone,
    provider_name character varying NOT NULL,
    provider_type character varying NOT NULL,
    pull_request_state character varying,
    repo_id uuid NOT NULL,
    served_at timestamp(6) without time zone,
    slug character varying NOT NULL,
    ssh_key text,
    state character varying DEFAULT 'new'::character varying NOT NULL,
    updated_at timestamp(6) without time zone NOT NULL,
    used_at timestamp(6) without time zone
);


--
-- Name: oban_jobs id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.oban_jobs ALTER COLUMN id SET DEFAULT nextval('public.oban_jobs_id_seq'::regclass);


--
-- Name: activities activities_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.activities
    ADD CONSTRAINT activities_pkey PRIMARY KEY (id);


--
-- Name: agent_session_states agent_session_states_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.agent_session_states
    ADD CONSTRAINT agent_session_states_pkey PRIMARY KEY (id);


--
-- Name: agent_sessions agent_sessions_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.agent_sessions
    ADD CONSTRAINT agent_sessions_pkey PRIMARY KEY (id);


--
-- Name: agent_skills agent_skills_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.agent_skills
    ADD CONSTRAINT agent_skills_pkey PRIMARY KEY (id);


--
-- Name: agents agents_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.agents
    ADD CONSTRAINT agents_pkey PRIMARY KEY (id);


--
-- Name: artifact_versions artifact_versions_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.artifact_versions
    ADD CONSTRAINT artifact_versions_pkey PRIMARY KEY (id);


--
-- Name: artifacts artifacts_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.artifacts
    ADD CONSTRAINT artifacts_pkey PRIMARY KEY (id);


--
-- Name: attachments attachments_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.attachments
    ADD CONSTRAINT attachments_pkey PRIMARY KEY (id);


--
-- Name: blobs blobs_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.blobs
    ADD CONSTRAINT blobs_pkey PRIMARY KEY (id);


--
-- Name: builders builders_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.builders
    ADD CONSTRAINT builders_pkey PRIMARY KEY (id);


--
-- Name: captures captures_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.captures
    ADD CONSTRAINT captures_pkey PRIMARY KEY (id);


--
-- Name: cloud_credentials cloud_credentials_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.cloud_credentials
    ADD CONSTRAINT cloud_credentials_pkey PRIMARY KEY (id);


--
-- Name: clusters clusters_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.clusters
    ADD CONSTRAINT clusters_pkey PRIMARY KEY (id);


--
-- Name: connectors connectors_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.connectors
    ADD CONSTRAINT connectors_pkey PRIMARY KEY (id);


--
-- Name: databases databases_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.databases
    ADD CONSTRAINT databases_pkey PRIMARY KEY (id);


--
-- Name: deployments deployments_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.deployments
    ADD CONSTRAINT deployments_pkey PRIMARY KEY (id);


--
-- Name: ecto_migrations ecto_migrations_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.ecto_migrations
    ADD CONSTRAINT ecto_migrations_pkey PRIMARY KEY (version);


--
-- Name: env_vars env_vars_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.env_vars
    ADD CONSTRAINT env_vars_pkey PRIMARY KEY (id);


--
-- Name: environment_commands environment_commands_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.environment_commands
    ADD CONSTRAINT environment_commands_pkey PRIMARY KEY (id);


--
-- Name: environments environments_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.environments
    ADD CONSTRAINT environments_pkey PRIMARY KEY (id);


--
-- Name: events events_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.events
    ADD CONSTRAINT events_pkey PRIMARY KEY (id);


--
-- Name: funnels funnels_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.funnels
    ADD CONSTRAINT funnels_pkey PRIMARY KEY (id);


--
-- Name: github_install_intents github_install_intents_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.github_install_intents
    ADD CONSTRAINT github_install_intents_pkey PRIMARY KEY (id);


--
-- Name: github_repositories github_repositories_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.github_repositories
    ADD CONSTRAINT github_repositories_pkey PRIMARY KEY (id);


--
-- Name: github_sign_ins github_sign_ins_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.github_sign_ins
    ADD CONSTRAINT github_sign_ins_pkey PRIMARY KEY (id);


--
-- Name: grants grants_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.grants
    ADD CONSTRAINT grants_pkey PRIMARY KEY (id);


--
-- Name: images images_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.images
    ADD CONSTRAINT images_pkey PRIMARY KEY (id);


--
-- Name: library_items library_items_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.library_items
    ADD CONSTRAINT library_items_pkey PRIMARY KEY (id);


--
-- Name: llm_configs llm_configs_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.llm_configs
    ADD CONSTRAINT llm_configs_pkey PRIMARY KEY (id);


--
-- Name: llm_connections llm_connections_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.llm_connections
    ADD CONSTRAINT llm_connections_pkey PRIMARY KEY (id);


--
-- Name: logs logs_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.logs
    ADD CONSTRAINT logs_pkey PRIMARY KEY (id);


--
-- Name: oban_jobs non_negative_priority; Type: CHECK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE public.oban_jobs
    ADD CONSTRAINT non_negative_priority CHECK ((priority >= 0)) NOT VALID;


--
-- Name: note_mentions note_mentions_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.note_mentions
    ADD CONSTRAINT note_mentions_pkey PRIMARY KEY (id);


--
-- Name: oauth_clients oauth_clients_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.oauth_clients
    ADD CONSTRAINT oauth_clients_pkey PRIMARY KEY (id);


--
-- Name: oauth_codes oauth_codes_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.oauth_codes
    ADD CONSTRAINT oauth_codes_pkey PRIMARY KEY (id);


--
-- Name: oban_jobs oban_jobs_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.oban_jobs
    ADD CONSTRAINT oban_jobs_pkey PRIMARY KEY (id);


--
-- Name: oban_peers oban_peers_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.oban_peers
    ADD CONSTRAINT oban_peers_pkey PRIMARY KEY (name);


--
-- Name: prices prices_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.prices
    ADD CONSTRAINT prices_pkey PRIMARY KEY (id);


--
-- Name: provisionings provisionings_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.provisionings
    ADD CONSTRAINT provisionings_pkey PRIMARY KEY (id);


--
-- Name: repos repos_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.repos
    ADD CONSTRAINT repos_pkey PRIMARY KEY (id);


--
-- Name: seeds seeds_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.seeds
    ADD CONSTRAINT seeds_pkey PRIMARY KEY (id);


--
-- Name: service_runs service_runs_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.service_runs
    ADD CONSTRAINT service_runs_pkey PRIMARY KEY (id);


--
-- Name: services services_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.services
    ADD CONSTRAINT services_pkey PRIMARY KEY (id);


--
-- Name: session_presences session_presences_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.session_presences
    ADD CONSTRAINT session_presences_pkey PRIMARY KEY (id);


--
-- Name: sessions sessions_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.sessions
    ADD CONSTRAINT sessions_pkey PRIMARY KEY (id);


--
-- Name: skill_files skill_files_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.skill_files
    ADD CONSTRAINT skill_files_pkey PRIMARY KEY (id);


--
-- Name: skills skills_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.skills
    ADD CONSTRAINT skills_pkey PRIMARY KEY (id);


--
-- Name: storage_costs storage_costs_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.storage_costs
    ADD CONSTRAINT storage_costs_pkey PRIMARY KEY (id);


--
-- Name: system_users system_users_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.system_users
    ADD CONSTRAINT system_users_pkey PRIMARY KEY (user_id);


--
-- Name: tasks tasks_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.tasks
    ADD CONSTRAINT tasks_pkey PRIMARY KEY (id);


--
-- Name: thumbnails thumbnails_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.thumbnails
    ADD CONSTRAINT thumbnails_pkey PRIMARY KEY (id);


--
-- Name: tool_grants tool_grants_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.tool_grants
    ADD CONSTRAINT tool_grants_pkey PRIMARY KEY (id);


--
-- Name: tool_presets tool_presets_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.tool_presets
    ADD CONSTRAINT tool_presets_pkey PRIMARY KEY (id);


--
-- Name: turns turns_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.turns
    ADD CONSTRAINT turns_pkey PRIMARY KEY (id);


--
-- Name: users users_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.users
    ADD CONSTRAINT users_pkey PRIMARY KEY (id);


--
-- Name: variants variants_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.variants
    ADD CONSTRAINT variants_pkey PRIMARY KEY (id);


--
-- Name: vm_costs vm_costs_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.vm_costs
    ADD CONSTRAINT vm_costs_pkey PRIMARY KEY (id);


--
-- Name: vm_snapshots vm_snapshots_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.vm_snapshots
    ADD CONSTRAINT vm_snapshots_pkey PRIMARY KEY (id);


--
-- Name: webhooks webhooks_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.webhooks
    ADD CONSTRAINT webhooks_pkey PRIMARY KEY (id);


--
-- Name: workflow_steps workflow_steps_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.workflow_steps
    ADD CONSTRAINT workflow_steps_pkey PRIMARY KEY (id);


--
-- Name: workflows workflows_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.workflows
    ADD CONSTRAINT workflows_pkey PRIMARY KEY (id);


--
-- Name: workspace_users workspace_users_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.workspace_users
    ADD CONSTRAINT workspace_users_pkey PRIMARY KEY (id);


--
-- Name: workspaces workspaces_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.workspaces
    ADD CONSTRAINT workspaces_pkey PRIMARY KEY (id);


--
-- Name: worktrees worktrees_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.worktrees
    ADD CONSTRAINT worktrees_pkey PRIMARY KEY (id);


--
-- Name: attachments_blob_id_index; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX attachments_blob_id_index ON public.attachments USING btree (blob_id);


--
-- Name: attachments_record_type_record_id_name_blob_id_index; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX attachments_record_type_record_id_name_blob_id_index ON public.attachments USING btree (record_type, record_id, name, blob_id);


--
-- Name: blobs_key_index; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX blobs_key_index ON public.blobs USING btree (key);


--
-- Name: builders_environment_id_index; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX builders_environment_id_index ON public.builders USING btree (environment_id);


--
-- Name: cloud_credentials_workspace_id_provider_index; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX cloud_credentials_workspace_id_provider_index ON public.cloud_credentials USING btree (workspace_id, provider);


--
-- Name: clusters_environment_id_index; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX clusters_environment_id_index ON public.clusters USING btree (environment_id);


--
-- Name: deployments_environment_id_created_at_index; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX deployments_environment_id_created_at_index ON public.deployments USING btree (environment_id, created_at);


--
-- Name: deployments_underway; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX deployments_underway ON public.deployments USING btree (environment_id) WHERE ((status)::text = ANY ((ARRAY['building'::character varying, 'deploying'::character varying])::text[]));


--
-- Name: idx_on_github_installation_id_github_id_392bbf93f5; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX idx_on_github_installation_id_github_id_392bbf93f5 ON public.github_repositories USING btree (github_installation_id, github_id);


--
-- Name: images_building; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX images_building ON public.images USING btree (environment_id) WHERE ((status)::text = 'building'::text);


--
-- Name: images_environment_id_commit_index; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX images_environment_id_commit_index ON public.images USING btree (environment_id, commit);


--
-- Name: index_activities_on_record; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX index_activities_on_record ON public.activities USING btree (record_type, record_id);


--
-- Name: index_activities_on_user_id_and_created_at; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX index_activities_on_user_id_and_created_at ON public.activities USING btree (user_id, created_at);


--
-- Name: index_activities_on_workspace_id_and_event_name_and_created_at; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX index_activities_on_workspace_id_and_event_name_and_created_at ON public.activities USING btree (workspace_id, event_name, created_at);


--
-- Name: index_agent_session_states_on_workspace_id_and_position; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX index_agent_session_states_on_workspace_id_and_position ON public.agent_session_states USING btree (workspace_id, "position");


--
-- Name: index_agent_sessions_on_agent_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX index_agent_sessions_on_agent_id ON public.agent_sessions USING btree (agent_id);


--
-- Name: index_agent_sessions_on_agent_session_state_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX index_agent_sessions_on_agent_session_state_id ON public.agent_sessions USING btree (agent_session_state_id);


--
-- Name: index_agent_sessions_on_llm_config_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX index_agent_sessions_on_llm_config_id ON public.agent_sessions USING btree (llm_config_id);


--
-- Name: index_agent_sessions_on_parent_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX index_agent_sessions_on_parent_id ON public.agent_sessions USING btree (parent_id);


--
-- Name: index_agent_sessions_on_task_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX index_agent_sessions_on_task_id ON public.agent_sessions USING btree (task_id);


--
-- Name: index_agent_sessions_on_task_id_and_created_at; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX index_agent_sessions_on_task_id_and_created_at ON public.agent_sessions USING btree (task_id, created_at);


--
-- Name: index_agent_sessions_on_terms; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX index_agent_sessions_on_terms ON public.agent_sessions USING gin (terms);


--
-- Name: index_agent_sessions_on_user_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX index_agent_sessions_on_user_id ON public.agent_sessions USING btree (user_id);


--
-- Name: index_agent_sessions_on_workflow_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX index_agent_sessions_on_workflow_id ON public.agent_sessions USING btree (workflow_id);


--
-- Name: index_agent_sessions_on_workflow_step_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX index_agent_sessions_on_workflow_step_id ON public.agent_sessions USING btree (workflow_step_id);


--
-- Name: index_agent_sessions_on_workspace_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX index_agent_sessions_on_workspace_id ON public.agent_sessions USING btree (workspace_id);


--
-- Name: index_agent_sessions_on_worktree_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX index_agent_sessions_on_worktree_id ON public.agent_sessions USING btree (worktree_id);


--
-- Name: index_agent_skills_on_agent_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX index_agent_skills_on_agent_id ON public.agent_skills USING btree (agent_id);


--
-- Name: index_agent_skills_on_agent_id_and_skill_id; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX index_agent_skills_on_agent_id_and_skill_id ON public.agent_skills USING btree (agent_id, skill_id);


--
-- Name: index_agent_skills_on_skill_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX index_agent_skills_on_skill_id ON public.agent_skills USING btree (skill_id);


--
-- Name: index_agents_on_llm_config_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX index_agents_on_llm_config_id ON public.agents USING btree (llm_config_id);


--
-- Name: index_agents_on_workspace_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX index_agents_on_workspace_id ON public.agents USING btree (workspace_id);


--
-- Name: index_agents_on_workspace_id_and_slug; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX index_agents_on_workspace_id_and_slug ON public.agents USING btree (workspace_id, slug);


--
-- Name: index_artifact_versions_on_artifact_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX index_artifact_versions_on_artifact_id ON public.artifact_versions USING btree (artifact_id);


--
-- Name: index_artifact_versions_on_artifact_id_and_number; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX index_artifact_versions_on_artifact_id_and_number ON public.artifact_versions USING btree (artifact_id, number);


--
-- Name: index_artifact_versions_on_terms; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX index_artifact_versions_on_terms ON public.artifact_versions USING gin (terms);


--
-- Name: index_artifact_versions_on_turn_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX index_artifact_versions_on_turn_id ON public.artifact_versions USING btree (turn_id);


--
-- Name: index_artifact_versions_on_user_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX index_artifact_versions_on_user_id ON public.artifact_versions USING btree (user_id);


--
-- Name: index_artifacts_on_session_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX index_artifacts_on_session_id ON public.artifacts USING btree (session_id);


--
-- Name: index_artifacts_on_slug; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX index_artifacts_on_slug ON public.artifacts USING btree (slug);


--
-- Name: index_artifacts_on_terms; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX index_artifacts_on_terms ON public.artifacts USING gin (terms);


--
-- Name: index_artifacts_on_workspace_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX index_artifacts_on_workspace_id ON public.artifacts USING btree (workspace_id);


--
-- Name: index_artifacts_on_workspace_id_and_name; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX index_artifacts_on_workspace_id_and_name ON public.artifacts USING btree (workspace_id, name);


--
-- Name: index_artifacts_on_worktree_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX index_artifacts_on_worktree_id ON public.artifacts USING btree (worktree_id);


--
-- Name: index_artifacts_on_worktree_id_and_port; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX index_artifacts_on_worktree_id_and_port ON public.artifacts USING btree (worktree_id, port);


--
-- Name: index_captures_on_digest; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX index_captures_on_digest ON public.captures USING btree (digest);


--
-- Name: index_captures_on_turn_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX index_captures_on_turn_id ON public.captures USING btree (turn_id);


--
-- Name: index_connectors_on_installer_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX index_connectors_on_installer_id ON public.connectors USING btree (installer_id);


--
-- Name: index_connectors_on_workspace_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX index_connectors_on_workspace_id ON public.connectors USING btree (workspace_id);


--
-- Name: index_connectors_on_workspace_id_and_source; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX index_connectors_on_workspace_id_and_source ON public.connectors USING btree (workspace_id, source);


--
-- Name: index_databases_on_environment_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX index_databases_on_environment_id ON public.databases USING btree (environment_id);


--
-- Name: index_databases_on_parent_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX index_databases_on_parent_id ON public.databases USING btree (parent_id);


--
-- Name: index_databases_on_worktree_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX index_databases_on_worktree_id ON public.databases USING btree (worktree_id);


--
-- Name: index_databases_one_branch; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX index_databases_one_branch ON public.databases USING btree (worktree_id) WHERE ((worktree_id IS NOT NULL) AND (archived_at IS NULL));


--
-- Name: index_databases_one_root; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX index_databases_one_root ON public.databases USING btree (environment_id) WHERE ((parent_id IS NULL) AND (archived_at IS NULL));


--
-- Name: index_env_vars_on_environment_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX index_env_vars_on_environment_id ON public.env_vars USING btree (environment_id);


--
-- Name: index_env_vars_on_environment_id_and_key; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX index_env_vars_on_environment_id_and_key ON public.env_vars USING btree (environment_id, key) WHERE (archived_at IS NULL);


--
-- Name: index_environment_commands_on_environment_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX index_environment_commands_on_environment_id ON public.environment_commands USING btree (environment_id);


--
-- Name: index_environment_commands_on_environment_id_and_name; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX index_environment_commands_on_environment_id_and_name ON public.environment_commands USING btree (environment_id, name) WHERE (archived_at IS NULL);


--
-- Name: index_environments_on_repo_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX index_environments_on_repo_id ON public.environments USING btree (repo_id);


--
-- Name: index_environments_on_repo_id_and_name; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX index_environments_on_repo_id_and_name ON public.environments USING btree (repo_id, name) WHERE (archived_at IS NULL);


--
-- Name: index_events_on_agent_session_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX index_events_on_agent_session_id ON public.events USING btree (agent_session_id);


--
-- Name: index_events_on_parent_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX index_events_on_parent_id ON public.events USING btree (parent_id);


--
-- Name: index_events_on_terms; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX index_events_on_terms ON public.events USING gin (to_tsvector('simple'::regconfig, public.unaccented("left"(COALESCE(content, ''::text), 100000)))) WHERE ((kind)::text = 'message'::text);


--
-- Name: index_events_on_turn_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX index_events_on_turn_id ON public.events USING btree (turn_id);


--
-- Name: index_events_on_turn_id_and_approval_status; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX index_events_on_turn_id_and_approval_status ON public.events USING btree (turn_id, approval_status);


--
-- Name: index_events_on_user_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX index_events_on_user_id ON public.events USING btree (user_id);


--
-- Name: index_funnels_on_key; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX index_funnels_on_key ON public.funnels USING btree (key);


--
-- Name: index_github_install_intents_on_user_id_and_created_at; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX index_github_install_intents_on_user_id_and_created_at ON public.github_install_intents USING btree (user_id, created_at);


--
-- Name: index_github_install_intents_on_workspace_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX index_github_install_intents_on_workspace_id ON public.github_install_intents USING btree (workspace_id);


--
-- Name: index_github_installations_app_free; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX index_github_installations_app_free ON public.connectors USING btree (github_installation_id) WHERE (((kind)::text = 'app'::text) AND (workspace_id IS NULL) AND (archived_at IS NULL));


--
-- Name: index_github_installations_app_workspace; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX index_github_installations_app_workspace ON public.connectors USING btree (github_installation_id, workspace_id) WHERE (((kind)::text = 'app'::text) AND (workspace_id IS NOT NULL) AND (archived_at IS NULL));


--
-- Name: index_github_installations_token_account; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX index_github_installations_token_account ON public.connectors USING btree (workspace_id, account_login) WHERE (((kind)::text = 'token'::text) AND (archived_at IS NULL));


--
-- Name: index_github_repositories_on_github_installation_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX index_github_repositories_on_github_installation_id ON public.github_repositories USING btree (github_installation_id);


--
-- Name: index_grants_on_environment_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX index_grants_on_environment_id ON public.grants USING btree (environment_id);


--
-- Name: index_grants_on_workspace_user_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX index_grants_on_workspace_user_id ON public.grants USING btree (workspace_user_id);


--
-- Name: index_grants_on_workspace_user_id_and_environment_id; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX index_grants_on_workspace_user_id_and_environment_id ON public.grants USING btree (workspace_user_id, environment_id);


--
-- Name: index_library_items_on_event_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX index_library_items_on_event_id ON public.library_items USING btree (event_id);


--
-- Name: index_library_items_on_terms; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX index_library_items_on_terms ON public.library_items USING gin (terms);


--
-- Name: index_library_items_on_workspace_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX index_library_items_on_workspace_id ON public.library_items USING btree (workspace_id);


--
-- Name: index_library_items_on_workspace_id_and_archived_at; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX index_library_items_on_workspace_id_and_archived_at ON public.library_items USING btree (workspace_id, archived_at);


--
-- Name: index_llm_configs_on_workspace_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX index_llm_configs_on_workspace_id ON public.llm_configs USING btree (workspace_id);


--
-- Name: index_llm_configs_on_workspace_id_and_label; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX index_llm_configs_on_workspace_id_and_label ON public.llm_configs USING btree (workspace_id, label) WHERE (archived_at IS NULL);


--
-- Name: index_llm_configs_one_main; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX index_llm_configs_one_main ON public.llm_configs USING btree (workspace_id) WHERE (main AND (archived_at IS NULL));


--
-- Name: index_llm_connections_on_llm_config_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX index_llm_connections_on_llm_config_id ON public.llm_connections USING btree (llm_config_id);


--
-- Name: index_llm_connections_on_state; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX index_llm_connections_on_state ON public.llm_connections USING btree (state);


--
-- Name: index_llm_connections_on_user_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX index_llm_connections_on_user_id ON public.llm_connections USING btree (user_id);


--
-- Name: index_llm_connections_on_workspace_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX index_llm_connections_on_workspace_id ON public.llm_connections USING btree (workspace_id);


--
-- Name: index_logs_on_record; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX index_logs_on_record ON public.logs USING btree (record_type, record_id);


--
-- Name: index_note_mentions_on_event_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX index_note_mentions_on_event_id ON public.note_mentions USING btree (event_id);


--
-- Name: index_note_mentions_on_event_id_and_user_id; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX index_note_mentions_on_event_id_and_user_id ON public.note_mentions USING btree (event_id, user_id);


--
-- Name: index_note_mentions_on_user_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX index_note_mentions_on_user_id ON public.note_mentions USING btree (user_id);


--
-- Name: index_note_mentions_on_user_id_and_handled_at; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX index_note_mentions_on_user_id_and_handled_at ON public.note_mentions USING btree (user_id, handled_at);


--
-- Name: index_prices_on_provider_name_and_key; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX index_prices_on_provider_name_and_key ON public.prices USING btree (provider_name, key);


--
-- Name: index_provisionings_on_worktree_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX index_provisionings_on_worktree_id ON public.provisionings USING btree (worktree_id);


--
-- Name: index_repos_on_github_installation_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX index_repos_on_github_installation_id ON public.repos USING btree (github_installation_id);


--
-- Name: index_repos_on_workspace_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX index_repos_on_workspace_id ON public.repos USING btree (workspace_id);


--
-- Name: index_repos_on_workspace_id_and_name; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX index_repos_on_workspace_id_and_name ON public.repos USING btree (workspace_id, name) WHERE (archived_at IS NULL);


--
-- Name: index_seeds_on_environment_id_and_provider_name; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX index_seeds_on_environment_id_and_provider_name ON public.seeds USING btree (environment_id, provider_name);


--
-- Name: index_session_presences_on_agent_session_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX index_session_presences_on_agent_session_id ON public.session_presences USING btree (agent_session_id);


--
-- Name: index_session_presences_on_agent_session_id_and_user_id; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX index_session_presences_on_agent_session_id_and_user_id ON public.session_presences USING btree (agent_session_id, user_id);


--
-- Name: index_session_presences_on_user_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX index_session_presences_on_user_id ON public.session_presences USING btree (user_id);


--
-- Name: index_sessions_on_user_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX index_sessions_on_user_id ON public.sessions USING btree (user_id);


--
-- Name: index_sessions_on_workspace_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX index_sessions_on_workspace_id ON public.sessions USING btree (workspace_id);


--
-- Name: index_skill_files_on_skill_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX index_skill_files_on_skill_id ON public.skill_files USING btree (skill_id);


--
-- Name: index_skills_on_workspace_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX index_skills_on_workspace_id ON public.skills USING btree (workspace_id);


--
-- Name: index_skills_on_workspace_id_and_slug; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX index_skills_on_workspace_id_and_slug ON public.skills USING btree (workspace_id, slug);


--
-- Name: index_tasks_on_agent_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX index_tasks_on_agent_id ON public.tasks USING btree (agent_id);


--
-- Name: index_tasks_on_environment_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX index_tasks_on_environment_id ON public.tasks USING btree (environment_id);


--
-- Name: index_tasks_on_next_run_at; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX index_tasks_on_next_run_at ON public.tasks USING btree (next_run_at) WHERE ((paused_at IS NULL) AND (archived_at IS NULL));


--
-- Name: index_tasks_on_source_and_event; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX index_tasks_on_source_and_event ON public.tasks USING btree (source, event) WHERE ((archived_at IS NULL) AND (paused_at IS NULL));


--
-- Name: index_tasks_on_workflow_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX index_tasks_on_workflow_id ON public.tasks USING btree (workflow_id);


--
-- Name: index_tasks_on_workspace_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX index_tasks_on_workspace_id ON public.tasks USING btree (workspace_id);


--
-- Name: index_thumbnails_on_workspace_id_and_digest; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX index_thumbnails_on_workspace_id_and_digest ON public.thumbnails USING btree (workspace_id, digest);


--
-- Name: index_tool_grants_on_workspace_user_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX index_tool_grants_on_workspace_user_id ON public.tool_grants USING btree (workspace_user_id);


--
-- Name: index_tool_grants_on_workspace_user_id_and_tool; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX index_tool_grants_on_workspace_user_id_and_tool ON public.tool_grants USING btree (workspace_user_id, tool);


--
-- Name: index_tool_presets_on_workspace_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX index_tool_presets_on_workspace_id ON public.tool_presets USING btree (workspace_id);


--
-- Name: index_tool_presets_on_workspace_id_and_role_and_tool; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX index_tool_presets_on_workspace_id_and_role_and_tool ON public.tool_presets USING btree (workspace_id, role, tool);


--
-- Name: index_turns_on_active_session; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX index_turns_on_active_session ON public.turns USING btree (agent_session_id) WHERE ((status)::text = ANY (ARRAY[('pending'::character varying)::text, ('running'::character varying)::text]));


--
-- Name: index_turns_on_agent_session_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX index_turns_on_agent_session_id ON public.turns USING btree (agent_session_id);


--
-- Name: index_turns_on_lead_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX index_turns_on_lead_id ON public.turns USING btree (lead_id);


--
-- Name: index_turns_on_note_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX index_turns_on_note_id ON public.turns USING btree (note_id);


--
-- Name: index_turns_on_terms; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX index_turns_on_terms ON public.turns USING gin (terms);


--
-- Name: index_turns_on_user_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX index_turns_on_user_id ON public.turns USING btree (user_id);


--
-- Name: index_users_on_email; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX index_users_on_email ON public.users USING btree (email);


--
-- Name: index_users_on_github_user_id; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX index_users_on_github_user_id ON public.users USING btree (github_user_id);


--
-- Name: index_vm_costs_on_owner_type_and_owner_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX index_vm_costs_on_owner_type_and_owner_id ON public.vm_costs USING btree (owner_type, owner_id);


--
-- Name: index_vm_costs_on_provider_name_and_box_id_and_day; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX index_vm_costs_on_provider_name_and_box_id_and_day ON public.vm_costs USING btree (provider_name, box_id, day);


--
-- Name: index_vm_costs_on_workspace_id_and_day; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX index_vm_costs_on_workspace_id_and_day ON public.vm_costs USING btree (workspace_id, day);


--
-- Name: index_vm_snapshots_on_costed_and_taken_at; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX index_vm_snapshots_on_costed_and_taken_at ON public.vm_snapshots USING btree (costed, taken_at);


--
-- Name: index_vm_snapshots_on_provider_name_and_box_id_and_taken_at; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX index_vm_snapshots_on_provider_name_and_box_id_and_taken_at ON public.vm_snapshots USING btree (provider_name, box_id, taken_at);


--
-- Name: index_webhooks_on_received_at; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX index_webhooks_on_received_at ON public.webhooks USING btree (received_at);


--
-- Name: index_webhooks_on_source_and_delivery_id; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX index_webhooks_on_source_and_delivery_id ON public.webhooks USING btree (source, delivery_id) WHERE (delivery_id IS NOT NULL);


--
-- Name: index_workflow_steps_on_agent_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX index_workflow_steps_on_agent_id ON public.workflow_steps USING btree (agent_id);


--
-- Name: index_workflow_steps_on_agent_session_state_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX index_workflow_steps_on_agent_session_state_id ON public.workflow_steps USING btree (agent_session_state_id);


--
-- Name: index_workflow_steps_on_workflow_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX index_workflow_steps_on_workflow_id ON public.workflow_steps USING btree (workflow_id);


--
-- Name: index_workflow_steps_on_workflow_id_and_position; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX index_workflow_steps_on_workflow_id_and_position ON public.workflow_steps USING btree (workflow_id, "position");


--
-- Name: index_workflows_on_environment_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX index_workflows_on_environment_id ON public.workflows USING btree (environment_id);


--
-- Name: index_workflows_on_workspace_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX index_workflows_on_workspace_id ON public.workflows USING btree (workspace_id);


--
-- Name: index_workspace_users_on_user_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX index_workspace_users_on_user_id ON public.workspace_users USING btree (user_id);


--
-- Name: index_workspace_users_on_workspace_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX index_workspace_users_on_workspace_id ON public.workspace_users USING btree (workspace_id);


--
-- Name: index_workspace_users_on_workspace_id_and_user_id; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX index_workspace_users_on_workspace_id_and_user_id ON public.workspace_users USING btree (workspace_id, user_id) WHERE (archived_at IS NULL);


--
-- Name: index_workspaces_on_slug; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX index_workspaces_on_slug ON public.workspaces USING btree (slug);


--
-- Name: index_worktrees_on_environment_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX index_worktrees_on_environment_id ON public.worktrees USING btree (environment_id);


--
-- Name: index_worktrees_on_repo_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX index_worktrees_on_repo_id ON public.worktrees USING btree (repo_id);


--
-- Name: index_worktrees_on_repo_id_and_branch; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX index_worktrees_on_repo_id_and_branch ON public.worktrees USING btree (repo_id, branch);


--
-- Name: index_worktrees_on_slug; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX index_worktrees_on_slug ON public.worktrees USING btree (slug);


--
-- Name: oauth_codes_code_digest_index; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX oauth_codes_code_digest_index ON public.oauth_codes USING btree (code_digest);


--
-- Name: oban_jobs_args_index; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX oban_jobs_args_index ON public.oban_jobs USING gin (args);


--
-- Name: oban_jobs_meta_index; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX oban_jobs_meta_index ON public.oban_jobs USING gin (meta);


--
-- Name: oban_jobs_state_cancelled_at_index; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX oban_jobs_state_cancelled_at_index ON public.oban_jobs USING btree (state, cancelled_at);


--
-- Name: oban_jobs_state_discarded_at_index; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX oban_jobs_state_discarded_at_index ON public.oban_jobs USING btree (state, discarded_at);


--
-- Name: oban_jobs_state_queue_priority_scheduled_at_id_index; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX oban_jobs_state_queue_priority_scheduled_at_id_index ON public.oban_jobs USING btree (state, queue, priority, scheduled_at, id);


--
-- Name: service_runs_environment_id_created_at_index; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX service_runs_environment_id_created_at_index ON public.service_runs USING btree (environment_id, created_at);


--
-- Name: services_environment_id_name_index; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX services_environment_id_name_index ON public.services USING btree (environment_id, name);


--
-- Name: sessions_refresh_digest_index; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX sessions_refresh_digest_index ON public.sessions USING btree (refresh_digest) WHERE (refresh_digest IS NOT NULL);


--
-- Name: storage_costs_workspace_id_category_environment_id_day_index; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX storage_costs_workspace_id_category_environment_id_day_index ON public.storage_costs USING btree (workspace_id, category, environment_id, day) NULLS NOT DISTINCT;


--
-- Name: variants_blob_id_variation_digest_index; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX variants_blob_id_variation_digest_index ON public.variants USING btree (blob_id, variation_digest);


--
-- Name: artifact_versions artifact_versions_noticed; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER artifact_versions_noticed AFTER INSERT OR DELETE OR UPDATE ON public.artifact_versions FOR EACH ROW EXECUTE FUNCTION public.turn_rows_noticed();


--
-- Name: artifacts artifacts_noticed; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER artifacts_noticed AFTER UPDATE OF name, kind ON public.artifacts FOR EACH ROW EXECUTE FUNCTION public.artifacts_noticed();


--
-- Name: attachments attachments_noticed; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER attachments_noticed AFTER INSERT OR DELETE OR UPDATE ON public.attachments FOR EACH ROW EXECUTE FUNCTION public.attachments_noticed();


--
-- Name: captures captures_noticed; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER captures_noticed AFTER INSERT OR DELETE OR UPDATE ON public.captures FOR EACH ROW EXECUTE FUNCTION public.turn_rows_noticed();


--
-- Name: deployments deployments_production; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER deployments_production BEFORE INSERT OR UPDATE OF environment_id ON public.deployments FOR EACH ROW EXECUTE FUNCTION public.deployments_production();


--
-- Name: environments environments_deployed; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER environments_deployed BEFORE UPDATE OF kind ON public.environments FOR EACH ROW EXECUTE FUNCTION public.environments_deployed();


--
-- Name: events events_noticed; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER events_noticed AFTER INSERT OR DELETE OR UPDATE ON public.events FOR EACH ROW EXECUTE FUNCTION public.turn_rows_noticed();


--
-- Name: images images_production; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER images_production BEFORE INSERT OR UPDATE OF environment_id ON public.images FOR EACH ROW EXECUTE FUNCTION public.images_production();


--
-- Name: thumbnails thumbnails_noticed; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER thumbnails_noticed AFTER INSERT OR UPDATE OF state ON public.thumbnails FOR EACH ROW EXECUTE FUNCTION public.thumbnails_noticed();


--
-- Name: turns turns_noticed; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER turns_noticed AFTER INSERT OR DELETE OR UPDATE OF status, prompt, lead_id, cents, input_tokens, output_tokens, cache_read_tokens, cache_write_tokens, conversation, note_id, user_id, internal ON public.turns FOR EACH ROW EXECUTE FUNCTION public.turns_noticed();


--
-- Name: agent_session_states agent_session_states_workspace_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.agent_session_states
    ADD CONSTRAINT agent_session_states_workspace_id_fkey FOREIGN KEY (workspace_id) REFERENCES public.workspaces(id);


--
-- Name: agent_sessions agent_sessions_agent_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.agent_sessions
    ADD CONSTRAINT agent_sessions_agent_id_fkey FOREIGN KEY (agent_id) REFERENCES public.agents(id);


--
-- Name: agent_sessions agent_sessions_agent_session_state_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.agent_sessions
    ADD CONSTRAINT agent_sessions_agent_session_state_id_fkey FOREIGN KEY (agent_session_state_id) REFERENCES public.agent_session_states(id) ON DELETE SET NULL;


--
-- Name: agent_sessions agent_sessions_llm_config_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.agent_sessions
    ADD CONSTRAINT agent_sessions_llm_config_id_fkey FOREIGN KEY (llm_config_id) REFERENCES public.llm_configs(id);


--
-- Name: agent_sessions agent_sessions_parent_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.agent_sessions
    ADD CONSTRAINT agent_sessions_parent_id_fkey FOREIGN KEY (parent_id) REFERENCES public.agent_sessions(id) ON DELETE CASCADE;


--
-- Name: agent_sessions agent_sessions_task_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.agent_sessions
    ADD CONSTRAINT agent_sessions_task_id_fkey FOREIGN KEY (task_id) REFERENCES public.tasks(id) ON DELETE SET NULL;


--
-- Name: agent_sessions agent_sessions_user_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.agent_sessions
    ADD CONSTRAINT agent_sessions_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE SET NULL;


--
-- Name: agent_sessions agent_sessions_workflow_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.agent_sessions
    ADD CONSTRAINT agent_sessions_workflow_id_fkey FOREIGN KEY (workflow_id) REFERENCES public.workflows(id) ON DELETE SET NULL;


--
-- Name: agent_sessions agent_sessions_workflow_step_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.agent_sessions
    ADD CONSTRAINT agent_sessions_workflow_step_id_fkey FOREIGN KEY (workflow_step_id) REFERENCES public.workflow_steps(id) ON DELETE SET NULL;


--
-- Name: agent_sessions agent_sessions_workspace_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.agent_sessions
    ADD CONSTRAINT agent_sessions_workspace_id_fkey FOREIGN KEY (workspace_id) REFERENCES public.workspaces(id) ON DELETE CASCADE;


--
-- Name: agent_sessions agent_sessions_worktree_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.agent_sessions
    ADD CONSTRAINT agent_sessions_worktree_id_fkey FOREIGN KEY (worktree_id) REFERENCES public.worktrees(id);


--
-- Name: agent_skills agent_skills_agent_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.agent_skills
    ADD CONSTRAINT agent_skills_agent_id_fkey FOREIGN KEY (agent_id) REFERENCES public.agents(id);


--
-- Name: agent_skills agent_skills_skill_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.agent_skills
    ADD CONSTRAINT agent_skills_skill_id_fkey FOREIGN KEY (skill_id) REFERENCES public.skills(id);


--
-- Name: agents agents_llm_config_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.agents
    ADD CONSTRAINT agents_llm_config_id_fkey FOREIGN KEY (llm_config_id) REFERENCES public.llm_configs(id) ON DELETE SET NULL;


--
-- Name: agents agents_workspace_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.agents
    ADD CONSTRAINT agents_workspace_id_fkey FOREIGN KEY (workspace_id) REFERENCES public.workspaces(id);


--
-- Name: artifact_versions artifact_versions_artifact_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.artifact_versions
    ADD CONSTRAINT artifact_versions_artifact_id_fkey FOREIGN KEY (artifact_id) REFERENCES public.artifacts(id);


--
-- Name: artifact_versions artifact_versions_turn_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.artifact_versions
    ADD CONSTRAINT artifact_versions_turn_id_fkey FOREIGN KEY (turn_id) REFERENCES public.turns(id) ON DELETE SET NULL;


--
-- Name: artifact_versions artifact_versions_user_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.artifact_versions
    ADD CONSTRAINT artifact_versions_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id);


--
-- Name: artifacts artifacts_session_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.artifacts
    ADD CONSTRAINT artifacts_session_id_fkey FOREIGN KEY (session_id) REFERENCES public.agent_sessions(id) ON DELETE SET NULL;


--
-- Name: artifacts artifacts_workspace_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.artifacts
    ADD CONSTRAINT artifacts_workspace_id_fkey FOREIGN KEY (workspace_id) REFERENCES public.workspaces(id);


--
-- Name: artifacts artifacts_worktree_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.artifacts
    ADD CONSTRAINT artifacts_worktree_id_fkey FOREIGN KEY (worktree_id) REFERENCES public.worktrees(id) ON DELETE SET NULL;


--
-- Name: attachments attachments_blob_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.attachments
    ADD CONSTRAINT attachments_blob_id_fkey FOREIGN KEY (blob_id) REFERENCES public.blobs(id);


--
-- Name: builders builders_environment_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.builders
    ADD CONSTRAINT builders_environment_id_fkey FOREIGN KEY (environment_id) REFERENCES public.environments(id) ON DELETE CASCADE;


--
-- Name: captures captures_turn_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.captures
    ADD CONSTRAINT captures_turn_id_fkey FOREIGN KEY (turn_id) REFERENCES public.turns(id);


--
-- Name: cloud_credentials cloud_credentials_workspace_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.cloud_credentials
    ADD CONSTRAINT cloud_credentials_workspace_id_fkey FOREIGN KEY (workspace_id) REFERENCES public.workspaces(id) ON DELETE CASCADE;


--
-- Name: clusters clusters_environment_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.clusters
    ADD CONSTRAINT clusters_environment_id_fkey FOREIGN KEY (environment_id) REFERENCES public.environments(id) ON DELETE RESTRICT;


--
-- Name: connectors connectors_installer_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.connectors
    ADD CONSTRAINT connectors_installer_id_fkey FOREIGN KEY (installer_id) REFERENCES public.users(id);


--
-- Name: connectors connectors_workspace_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.connectors
    ADD CONSTRAINT connectors_workspace_id_fkey FOREIGN KEY (workspace_id) REFERENCES public.workspaces(id);


--
-- Name: databases databases_environment_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.databases
    ADD CONSTRAINT databases_environment_id_fkey FOREIGN KEY (environment_id) REFERENCES public.environments(id) ON DELETE CASCADE;


--
-- Name: databases databases_parent_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.databases
    ADD CONSTRAINT databases_parent_id_fkey FOREIGN KEY (parent_id) REFERENCES public.databases(id) ON DELETE CASCADE;


--
-- Name: databases databases_worktree_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.databases
    ADD CONSTRAINT databases_worktree_id_fkey FOREIGN KEY (worktree_id) REFERENCES public.worktrees(id) ON DELETE SET NULL;


--
-- Name: deployments deployments_allowed_by_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.deployments
    ADD CONSTRAINT deployments_allowed_by_id_fkey FOREIGN KEY (allowed_by_id) REFERENCES public.users(id) ON DELETE SET NULL;


--
-- Name: deployments deployments_environment_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.deployments
    ADD CONSTRAINT deployments_environment_id_fkey FOREIGN KEY (environment_id) REFERENCES public.environments(id) ON DELETE RESTRICT;


--
-- Name: deployments deployments_image_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.deployments
    ADD CONSTRAINT deployments_image_id_fkey FOREIGN KEY (image_id) REFERENCES public.images(id) ON DELETE SET NULL;


--
-- Name: deployments deployments_rollback_of_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.deployments
    ADD CONSTRAINT deployments_rollback_of_id_fkey FOREIGN KEY (rollback_of_id) REFERENCES public.deployments(id) ON DELETE SET NULL;


--
-- Name: deployments deployments_started_by_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.deployments
    ADD CONSTRAINT deployments_started_by_id_fkey FOREIGN KEY (started_by_id) REFERENCES public.users(id) ON DELETE SET NULL;


--
-- Name: env_vars env_vars_environment_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.env_vars
    ADD CONSTRAINT env_vars_environment_id_fkey FOREIGN KEY (environment_id) REFERENCES public.environments(id);


--
-- Name: environment_commands environment_commands_environment_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.environment_commands
    ADD CONSTRAINT environment_commands_environment_id_fkey FOREIGN KEY (environment_id) REFERENCES public.environments(id);


--
-- Name: environments environments_repo_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.environments
    ADD CONSTRAINT environments_repo_id_fkey FOREIGN KEY (repo_id) REFERENCES public.repos(id);


--
-- Name: events events_agent_session_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.events
    ADD CONSTRAINT events_agent_session_id_fkey FOREIGN KEY (agent_session_id) REFERENCES public.agent_sessions(id) ON DELETE CASCADE;


--
-- Name: events events_parent_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.events
    ADD CONSTRAINT events_parent_id_fkey FOREIGN KEY (parent_id) REFERENCES public.events(id) ON DELETE CASCADE;


--
-- Name: events events_turn_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.events
    ADD CONSTRAINT events_turn_id_fkey FOREIGN KEY (turn_id) REFERENCES public.turns(id);


--
-- Name: events events_user_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.events
    ADD CONSTRAINT events_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE SET NULL;


--
-- Name: github_install_intents github_install_intents_user_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.github_install_intents
    ADD CONSTRAINT github_install_intents_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;


--
-- Name: github_install_intents github_install_intents_workspace_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.github_install_intents
    ADD CONSTRAINT github_install_intents_workspace_id_fkey FOREIGN KEY (workspace_id) REFERENCES public.workspaces(id) ON DELETE CASCADE;


--
-- Name: github_repositories github_repositories_github_installation_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.github_repositories
    ADD CONSTRAINT github_repositories_github_installation_id_fkey FOREIGN KEY (github_installation_id) REFERENCES public.connectors(id);


--
-- Name: github_sign_ins github_sign_ins_user_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.github_sign_ins
    ADD CONSTRAINT github_sign_ins_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;


--
-- Name: github_sign_ins github_sign_ins_workspace_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.github_sign_ins
    ADD CONSTRAINT github_sign_ins_workspace_id_fkey FOREIGN KEY (workspace_id) REFERENCES public.workspaces(id) ON DELETE CASCADE;


--
-- Name: grants grants_environment_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.grants
    ADD CONSTRAINT grants_environment_id_fkey FOREIGN KEY (environment_id) REFERENCES public.environments(id) ON DELETE CASCADE;


--
-- Name: grants grants_workspace_user_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.grants
    ADD CONSTRAINT grants_workspace_user_id_fkey FOREIGN KEY (workspace_user_id) REFERENCES public.workspace_users(id) ON DELETE CASCADE;


--
-- Name: images images_agent_session_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.images
    ADD CONSTRAINT images_agent_session_id_fkey FOREIGN KEY (agent_session_id) REFERENCES public.agent_sessions(id) ON DELETE SET NULL;


--
-- Name: images images_builder_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.images
    ADD CONSTRAINT images_builder_id_fkey FOREIGN KEY (builder_id) REFERENCES public.builders(id) ON DELETE SET NULL;


--
-- Name: images images_environment_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.images
    ADD CONSTRAINT images_environment_id_fkey FOREIGN KEY (environment_id) REFERENCES public.environments(id) ON DELETE RESTRICT;


--
-- Name: images images_user_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.images
    ADD CONSTRAINT images_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE SET NULL;


--
-- Name: library_items library_items_event_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.library_items
    ADD CONSTRAINT library_items_event_id_fkey FOREIGN KEY (event_id) REFERENCES public.events(id) ON DELETE SET NULL;


--
-- Name: library_items library_items_workspace_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.library_items
    ADD CONSTRAINT library_items_workspace_id_fkey FOREIGN KEY (workspace_id) REFERENCES public.workspaces(id);


--
-- Name: llm_configs llm_configs_workspace_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.llm_configs
    ADD CONSTRAINT llm_configs_workspace_id_fkey FOREIGN KEY (workspace_id) REFERENCES public.workspaces(id);


--
-- Name: llm_connections llm_connections_llm_config_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.llm_connections
    ADD CONSTRAINT llm_connections_llm_config_id_fkey FOREIGN KEY (llm_config_id) REFERENCES public.llm_configs(id) ON DELETE SET NULL;


--
-- Name: llm_connections llm_connections_user_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.llm_connections
    ADD CONSTRAINT llm_connections_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;


--
-- Name: llm_connections llm_connections_workspace_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.llm_connections
    ADD CONSTRAINT llm_connections_workspace_id_fkey FOREIGN KEY (workspace_id) REFERENCES public.workspaces(id) ON DELETE CASCADE;


--
-- Name: note_mentions note_mentions_event_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.note_mentions
    ADD CONSTRAINT note_mentions_event_id_fkey FOREIGN KEY (event_id) REFERENCES public.events(id) ON DELETE CASCADE;


--
-- Name: note_mentions note_mentions_user_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.note_mentions
    ADD CONSTRAINT note_mentions_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;


--
-- Name: oauth_codes oauth_codes_oauth_client_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.oauth_codes
    ADD CONSTRAINT oauth_codes_oauth_client_id_fkey FOREIGN KEY (oauth_client_id) REFERENCES public.oauth_clients(id) ON DELETE CASCADE;


--
-- Name: oauth_codes oauth_codes_user_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.oauth_codes
    ADD CONSTRAINT oauth_codes_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;


--
-- Name: oauth_codes oauth_codes_workspace_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.oauth_codes
    ADD CONSTRAINT oauth_codes_workspace_id_fkey FOREIGN KEY (workspace_id) REFERENCES public.workspaces(id) ON DELETE CASCADE;


--
-- Name: provisionings provisionings_worktree_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.provisionings
    ADD CONSTRAINT provisionings_worktree_id_fkey FOREIGN KEY (worktree_id) REFERENCES public.worktrees(id);


--
-- Name: repos repos_github_installation_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.repos
    ADD CONSTRAINT repos_github_installation_id_fkey FOREIGN KEY (github_installation_id) REFERENCES public.connectors(id) ON DELETE SET NULL;


--
-- Name: repos repos_workspace_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.repos
    ADD CONSTRAINT repos_workspace_id_fkey FOREIGN KEY (workspace_id) REFERENCES public.workspaces(id);


--
-- Name: seeds seeds_environment_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.seeds
    ADD CONSTRAINT seeds_environment_id_fkey FOREIGN KEY (environment_id) REFERENCES public.environments(id);


--
-- Name: service_runs service_runs_environment_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.service_runs
    ADD CONSTRAINT service_runs_environment_id_fkey FOREIGN KEY (environment_id) REFERENCES public.environments(id) ON DELETE CASCADE;


--
-- Name: service_runs service_runs_user_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.service_runs
    ADD CONSTRAINT service_runs_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE SET NULL;


--
-- Name: services services_environment_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.services
    ADD CONSTRAINT services_environment_id_fkey FOREIGN KEY (environment_id) REFERENCES public.environments(id) ON DELETE CASCADE;


--
-- Name: session_presences session_presences_agent_session_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.session_presences
    ADD CONSTRAINT session_presences_agent_session_id_fkey FOREIGN KEY (agent_session_id) REFERENCES public.agent_sessions(id) ON DELETE CASCADE;


--
-- Name: session_presences session_presences_user_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.session_presences
    ADD CONSTRAINT session_presences_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;


--
-- Name: sessions sessions_oauth_client_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.sessions
    ADD CONSTRAINT sessions_oauth_client_id_fkey FOREIGN KEY (oauth_client_id) REFERENCES public.oauth_clients(id) ON DELETE CASCADE;


--
-- Name: sessions sessions_user_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.sessions
    ADD CONSTRAINT sessions_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id);


--
-- Name: sessions sessions_workspace_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.sessions
    ADD CONSTRAINT sessions_workspace_id_fkey FOREIGN KEY (workspace_id) REFERENCES public.workspaces(id);


--
-- Name: skill_files skill_files_skill_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.skill_files
    ADD CONSTRAINT skill_files_skill_id_fkey FOREIGN KEY (skill_id) REFERENCES public.skills(id);


--
-- Name: skills skills_workspace_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.skills
    ADD CONSTRAINT skills_workspace_id_fkey FOREIGN KEY (workspace_id) REFERENCES public.workspaces(id);


--
-- Name: storage_costs storage_costs_environment_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.storage_costs
    ADD CONSTRAINT storage_costs_environment_id_fkey FOREIGN KEY (environment_id) REFERENCES public.environments(id) ON DELETE SET NULL;


--
-- Name: storage_costs storage_costs_workspace_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.storage_costs
    ADD CONSTRAINT storage_costs_workspace_id_fkey FOREIGN KEY (workspace_id) REFERENCES public.workspaces(id) ON DELETE CASCADE;


--
-- Name: system_users system_users_user_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.system_users
    ADD CONSTRAINT system_users_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;


--
-- Name: tasks tasks_agent_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.tasks
    ADD CONSTRAINT tasks_agent_id_fkey FOREIGN KEY (agent_id) REFERENCES public.agents(id);


--
-- Name: tasks tasks_environment_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.tasks
    ADD CONSTRAINT tasks_environment_id_fkey FOREIGN KEY (environment_id) REFERENCES public.environments(id);


--
-- Name: tasks tasks_workflow_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.tasks
    ADD CONSTRAINT tasks_workflow_id_fkey FOREIGN KEY (workflow_id) REFERENCES public.workflows(id);


--
-- Name: tasks tasks_workspace_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.tasks
    ADD CONSTRAINT tasks_workspace_id_fkey FOREIGN KEY (workspace_id) REFERENCES public.workspaces(id);


--
-- Name: thumbnails thumbnails_workspace_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.thumbnails
    ADD CONSTRAINT thumbnails_workspace_id_fkey FOREIGN KEY (workspace_id) REFERENCES public.workspaces(id);


--
-- Name: tool_grants tool_grants_workspace_user_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.tool_grants
    ADD CONSTRAINT tool_grants_workspace_user_id_fkey FOREIGN KEY (workspace_user_id) REFERENCES public.workspace_users(id) ON DELETE CASCADE;


--
-- Name: tool_presets tool_presets_workspace_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.tool_presets
    ADD CONSTRAINT tool_presets_workspace_id_fkey FOREIGN KEY (workspace_id) REFERENCES public.workspaces(id) ON DELETE CASCADE;


--
-- Name: turns turns_agent_session_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.turns
    ADD CONSTRAINT turns_agent_session_id_fkey FOREIGN KEY (agent_session_id) REFERENCES public.agent_sessions(id);


--
-- Name: turns turns_lead_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.turns
    ADD CONSTRAINT turns_lead_id_fkey FOREIGN KEY (lead_id) REFERENCES public.turns(id) ON DELETE CASCADE;


--
-- Name: turns turns_note_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.turns
    ADD CONSTRAINT turns_note_id_fkey FOREIGN KEY (note_id) REFERENCES public.events(id) ON DELETE SET NULL;


--
-- Name: turns turns_user_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.turns
    ADD CONSTRAINT turns_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE SET NULL;


--
-- Name: variants variants_blob_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.variants
    ADD CONSTRAINT variants_blob_id_fkey FOREIGN KEY (blob_id) REFERENCES public.blobs(id);


--
-- Name: vm_costs vm_costs_workspace_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.vm_costs
    ADD CONSTRAINT vm_costs_workspace_id_fkey FOREIGN KEY (workspace_id) REFERENCES public.workspaces(id);


--
-- Name: vm_snapshots vm_snapshots_workspace_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.vm_snapshots
    ADD CONSTRAINT vm_snapshots_workspace_id_fkey FOREIGN KEY (workspace_id) REFERENCES public.workspaces(id);


--
-- Name: workflow_steps workflow_steps_agent_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.workflow_steps
    ADD CONSTRAINT workflow_steps_agent_id_fkey FOREIGN KEY (agent_id) REFERENCES public.agents(id);


--
-- Name: workflow_steps workflow_steps_agent_session_state_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.workflow_steps
    ADD CONSTRAINT workflow_steps_agent_session_state_id_fkey FOREIGN KEY (agent_session_state_id) REFERENCES public.agent_session_states(id) ON DELETE SET NULL;


--
-- Name: workflow_steps workflow_steps_workflow_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.workflow_steps
    ADD CONSTRAINT workflow_steps_workflow_id_fkey FOREIGN KEY (workflow_id) REFERENCES public.workflows(id) ON DELETE CASCADE;


--
-- Name: workflows workflows_environment_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.workflows
    ADD CONSTRAINT workflows_environment_id_fkey FOREIGN KEY (environment_id) REFERENCES public.environments(id) ON DELETE SET NULL;


--
-- Name: workflows workflows_workspace_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.workflows
    ADD CONSTRAINT workflows_workspace_id_fkey FOREIGN KEY (workspace_id) REFERENCES public.workspaces(id);


--
-- Name: workspace_users workspace_users_user_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.workspace_users
    ADD CONSTRAINT workspace_users_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id);


--
-- Name: workspace_users workspace_users_workspace_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.workspace_users
    ADD CONSTRAINT workspace_users_workspace_id_fkey FOREIGN KEY (workspace_id) REFERENCES public.workspaces(id);


--
-- Name: worktrees worktrees_environment_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.worktrees
    ADD CONSTRAINT worktrees_environment_id_fkey FOREIGN KEY (environment_id) REFERENCES public.environments(id);


--
-- Name: worktrees worktrees_repo_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.worktrees
    ADD CONSTRAINT worktrees_repo_id_fkey FOREIGN KEY (repo_id) REFERENCES public.repos(id);


--
-- PostgreSQL database dump complete
--


INSERT INTO public."ecto_migrations" (version) VALUES (20261006000000);
INSERT INTO public."ecto_migrations" (version) VALUES (20261006180000);
INSERT INTO public."ecto_migrations" (version) VALUES (20261007000000);
INSERT INTO public."ecto_migrations" (version) VALUES (20261007000100);
INSERT INTO public."ecto_migrations" (version) VALUES (20261007200000);
INSERT INTO public."ecto_migrations" (version) VALUES (20261008000000);
INSERT INTO public."ecto_migrations" (version) VALUES (20261008000100);
INSERT INTO public."ecto_migrations" (version) VALUES (20261009000000);
INSERT INTO public."ecto_migrations" (version) VALUES (20261010000000);
INSERT INTO public."ecto_migrations" (version) VALUES (20261010000100);
INSERT INTO public."ecto_migrations" (version) VALUES (20261011000000);
INSERT INTO public."ecto_migrations" (version) VALUES (20261012000000);
INSERT INTO public."ecto_migrations" (version) VALUES (20261012000100);
INSERT INTO public."ecto_migrations" (version) VALUES (20261013000000);
INSERT INTO public."ecto_migrations" (version) VALUES (20261013000100);
INSERT INTO public."ecto_migrations" (version) VALUES (20261013000200);
INSERT INTO public."ecto_migrations" (version) VALUES (20261013000300);
INSERT INTO public."ecto_migrations" (version) VALUES (20261013000400);
INSERT INTO public."ecto_migrations" (version) VALUES (20261013000500);
INSERT INTO public."ecto_migrations" (version) VALUES (20261013000600);
INSERT INTO public."ecto_migrations" (version) VALUES (20261013000700);
INSERT INTO public."ecto_migrations" (version) VALUES (20261013000800);
INSERT INTO public."ecto_migrations" (version) VALUES (20261013000900);
INSERT INTO public."ecto_migrations" (version) VALUES (20261014000100);
INSERT INTO public."ecto_migrations" (version) VALUES (20261015000000);
INSERT INTO public."ecto_migrations" (version) VALUES (20261015000200);
INSERT INTO public."ecto_migrations" (version) VALUES (20261016000000);
