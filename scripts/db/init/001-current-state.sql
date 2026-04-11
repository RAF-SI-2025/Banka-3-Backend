--
-- PostgreSQL database dump
--

\restrict nIbCOnMOZvfbTJURECJQ3Nd0vODoWTTFYxbD6hBW8gg2hDohDedMzaOGpRj7beX

-- Dumped from database version 18.3
-- Dumped by pg_dump version 18.3

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

ALTER TABLE IF EXISTS ONLY public.verification_codes DROP CONSTRAINT IF EXISTS verification_codes_client_id_fkey;
ALTER TABLE IF EXISTS ONLY public.transfers DROP CONSTRAINT IF EXISTS transfers_to_account_fkey;
ALTER TABLE IF EXISTS ONLY public.transfers DROP CONSTRAINT IF EXISTS transfers_start_currency_id_fkey;
ALTER TABLE IF EXISTS ONLY public.transfers DROP CONSTRAINT IF EXISTS transfers_from_account_fkey;
ALTER TABLE IF EXISTS ONLY public.transaction_verification_codes DROP CONSTRAINT IF EXISTS transaction_verification_codes_client_id_fkey;
ALTER TABLE IF EXISTS ONLY public.payments DROP CONSTRAINT IF EXISTS payments_to_account_fkey;
ALTER TABLE IF EXISTS ONLY public.payments DROP CONSTRAINT IF EXISTS payments_recipient_id_fkey;
ALTER TABLE IF EXISTS ONLY public.payments DROP CONSTRAINT IF EXISTS payments_from_account_fkey;
ALTER TABLE IF EXISTS ONLY public.payment_recipients DROP CONSTRAINT IF EXISTS payment_recipients_client_id_fkey;
ALTER TABLE IF EXISTS ONLY public.loans DROP CONSTRAINT IF EXISTS loans_currency_id_fkey;
ALTER TABLE IF EXISTS ONLY public.loans DROP CONSTRAINT IF EXISTS loans_account_id_fkey;
ALTER TABLE IF EXISTS ONLY public.loan_request DROP CONSTRAINT IF EXISTS loan_request_currency_id_fkey;
ALTER TABLE IF EXISTS ONLY public.loan_request DROP CONSTRAINT IF EXISTS loan_request_account_id_fkey;
ALTER TABLE IF EXISTS ONLY public.loan_installment DROP CONSTRAINT IF EXISTS loan_installment_loan_id_fkey;
ALTER TABLE IF EXISTS ONLY public.loan_installment DROP CONSTRAINT IF EXISTS loan_installment_currency_id_fkey;
ALTER TABLE IF EXISTS ONLY public.employee_permissions DROP CONSTRAINT IF EXISTS employee_permissions_permission_id_fkey;
ALTER TABLE IF EXISTS ONLY public.employee_permissions DROP CONSTRAINT IF EXISTS employee_permissions_employee_id_fkey;
ALTER TABLE IF EXISTS ONLY public.companies DROP CONSTRAINT IF EXISTS companies_owner_id_fkey;
ALTER TABLE IF EXISTS ONLY public.companies DROP CONSTRAINT IF EXISTS companies_activity_code_id_fkey;
ALTER TABLE IF EXISTS ONLY public.cards DROP CONSTRAINT IF EXISTS cards_account_number_fkey;
ALTER TABLE IF EXISTS ONLY public.card_requests DROP CONSTRAINT IF EXISTS card_requests_account_number_fkey;
ALTER TABLE IF EXISTS ONLY public.backup_codes DROP CONSTRAINT IF EXISTS backup_codes_client_id_fkey;
ALTER TABLE IF EXISTS ONLY public.accounts DROP CONSTRAINT IF EXISTS accounts_owner_fkey;
ALTER TABLE IF EXISTS ONLY public.accounts DROP CONSTRAINT IF EXISTS accounts_currency_fkey;
ALTER TABLE IF EXISTS ONLY public.accounts DROP CONSTRAINT IF EXISTS accounts_created_by_fkey;
DROP TRIGGER IF EXISTS trg_permission_change ON public.employee_permissions;
DROP TRIGGER IF EXISTS trg_employee_status_change ON public.employees;
DROP INDEX IF EXISTS public.idx_refresh_tokens_email;
ALTER TABLE IF EXISTS ONLY public.verification_codes DROP CONSTRAINT IF EXISTS verification_codes_pkey;
ALTER TABLE IF EXISTS ONLY public.transfers DROP CONSTRAINT IF EXISTS transfers_pkey;
ALTER TABLE IF EXISTS ONLY public.transaction_verification_codes DROP CONSTRAINT IF EXISTS transaction_verification_codes_pkey;
ALTER TABLE IF EXISTS ONLY public.refresh_tokens DROP CONSTRAINT IF EXISTS refresh_tokens_pkey;
ALTER TABLE IF EXISTS ONLY public.permissions DROP CONSTRAINT IF EXISTS permissions_pkey;
ALTER TABLE IF EXISTS ONLY public.permissions DROP CONSTRAINT IF EXISTS permissions_name_key;
ALTER TABLE IF EXISTS ONLY public.payments DROP CONSTRAINT IF EXISTS payments_pkey;
ALTER TABLE IF EXISTS ONLY public.payment_recipients DROP CONSTRAINT IF EXISTS payment_recipients_pkey;
ALTER TABLE IF EXISTS ONLY public.payment_recipients DROP CONSTRAINT IF EXISTS payment_recipients_client_id_account_number_key;
ALTER TABLE IF EXISTS ONLY public.payment_codes DROP CONSTRAINT IF EXISTS payment_codes_pkey;
ALTER TABLE IF EXISTS ONLY public.password_action_tokens DROP CONSTRAINT IF EXISTS password_action_tokens_pkey;
ALTER TABLE IF EXISTS ONLY public.password_action_tokens DROP CONSTRAINT IF EXISTS password_action_tokens_hashed_token_key;
ALTER TABLE IF EXISTS ONLY public.loans DROP CONSTRAINT IF EXISTS loans_pkey;
ALTER TABLE IF EXISTS ONLY public.loan_request DROP CONSTRAINT IF EXISTS loan_request_pkey;
ALTER TABLE IF EXISTS ONLY public.loan_installment DROP CONSTRAINT IF EXISTS loan_installment_pkey;
ALTER TABLE IF EXISTS ONLY public.exchange_rates DROP CONSTRAINT IF EXISTS exchange_rates_pkey;
ALTER TABLE IF EXISTS ONLY public.employees DROP CONSTRAINT IF EXISTS employees_username_key;
ALTER TABLE IF EXISTS ONLY public.employees DROP CONSTRAINT IF EXISTS employees_pkey;
ALTER TABLE IF EXISTS ONLY public.employees DROP CONSTRAINT IF EXISTS employees_email_key;
ALTER TABLE IF EXISTS ONLY public.employee_permissions DROP CONSTRAINT IF EXISTS employee_permissions_pkey;
ALTER TABLE IF EXISTS ONLY public.currencies DROP CONSTRAINT IF EXISTS currencies_pkey;
ALTER TABLE IF EXISTS ONLY public.currencies DROP CONSTRAINT IF EXISTS currencies_label_key;
ALTER TABLE IF EXISTS ONLY public.companies DROP CONSTRAINT IF EXISTS companies_tax_code_key;
ALTER TABLE IF EXISTS ONLY public.companies DROP CONSTRAINT IF EXISTS companies_registered_id_key;
ALTER TABLE IF EXISTS ONLY public.companies DROP CONSTRAINT IF EXISTS companies_pkey;
ALTER TABLE IF EXISTS ONLY public.clients DROP CONSTRAINT IF EXISTS clients_pkey;
ALTER TABLE IF EXISTS ONLY public.clients DROP CONSTRAINT IF EXISTS clients_email_key;
ALTER TABLE IF EXISTS ONLY public.cards DROP CONSTRAINT IF EXISTS cards_pkey;
ALTER TABLE IF EXISTS ONLY public.cards DROP CONSTRAINT IF EXISTS cards_number_key;
ALTER TABLE IF EXISTS ONLY public.card_requests DROP CONSTRAINT IF EXISTS card_requests_pkey;
ALTER TABLE IF EXISTS ONLY public.authorized_party DROP CONSTRAINT IF EXISTS authorized_party_pkey;
ALTER TABLE IF EXISTS ONLY public.activity_codes DROP CONSTRAINT IF EXISTS activity_codes_pkey;
ALTER TABLE IF EXISTS ONLY public.activity_codes DROP CONSTRAINT IF EXISTS activity_codes_code_key;
ALTER TABLE IF EXISTS ONLY public.accounts DROP CONSTRAINT IF EXISTS accounts_pkey;
ALTER TABLE IF EXISTS ONLY public.accounts DROP CONSTRAINT IF EXISTS accounts_number_key;
ALTER TABLE IF EXISTS public.transfers ALTER COLUMN transaction_id DROP DEFAULT;
ALTER TABLE IF EXISTS public.permissions ALTER COLUMN id DROP DEFAULT;
ALTER TABLE IF EXISTS public.payments ALTER COLUMN transaction_id DROP DEFAULT;
ALTER TABLE IF EXISTS public.payment_recipients ALTER COLUMN id DROP DEFAULT;
ALTER TABLE IF EXISTS public.loans ALTER COLUMN currency_id DROP DEFAULT;
ALTER TABLE IF EXISTS public.loans ALTER COLUMN id DROP DEFAULT;
ALTER TABLE IF EXISTS public.loan_request ALTER COLUMN id DROP DEFAULT;
ALTER TABLE IF EXISTS public.loan_installment ALTER COLUMN currency_id DROP DEFAULT;
ALTER TABLE IF EXISTS public.loan_installment ALTER COLUMN id DROP DEFAULT;
ALTER TABLE IF EXISTS public.employees ALTER COLUMN id DROP DEFAULT;
ALTER TABLE IF EXISTS public.currencies ALTER COLUMN id DROP DEFAULT;
ALTER TABLE IF EXISTS public.companies ALTER COLUMN id DROP DEFAULT;
ALTER TABLE IF EXISTS public.clients ALTER COLUMN id DROP DEFAULT;
ALTER TABLE IF EXISTS public.cards ALTER COLUMN id DROP DEFAULT;
ALTER TABLE IF EXISTS public.card_requests ALTER COLUMN id DROP DEFAULT;
ALTER TABLE IF EXISTS public.authorized_party ALTER COLUMN id DROP DEFAULT;
ALTER TABLE IF EXISTS public.activity_codes ALTER COLUMN id DROP DEFAULT;
ALTER TABLE IF EXISTS public.accounts ALTER COLUMN id DROP DEFAULT;
DROP TABLE IF EXISTS public.verification_codes;
DROP SEQUENCE IF EXISTS public.transfers_transaction_id_seq;
DROP TABLE IF EXISTS public.transfers;
DROP TABLE IF EXISTS public.transaction_verification_codes;
DROP TABLE IF EXISTS public.refresh_tokens;
DROP SEQUENCE IF EXISTS public.permissions_id_seq;
DROP TABLE IF EXISTS public.permissions;
DROP SEQUENCE IF EXISTS public.payments_transaction_id_seq;
DROP TABLE IF EXISTS public.payments;
DROP SEQUENCE IF EXISTS public.payment_recipients_id_seq;
DROP TABLE IF EXISTS public.payment_recipients;
DROP TABLE IF EXISTS public.payment_codes;
DROP TABLE IF EXISTS public.password_action_tokens;
DROP SEQUENCE IF EXISTS public.loans_id_seq;
DROP SEQUENCE IF EXISTS public.loans_currency_id_seq;
DROP TABLE IF EXISTS public.loans;
DROP SEQUENCE IF EXISTS public.loan_request_id_seq;
DROP TABLE IF EXISTS public.loan_request;
DROP SEQUENCE IF EXISTS public.loan_installment_id_seq;
DROP SEQUENCE IF EXISTS public.loan_installment_currency_id_seq;
DROP TABLE IF EXISTS public.loan_installment;
DROP TABLE IF EXISTS public.exchange_rates;
DROP SEQUENCE IF EXISTS public.employees_id_seq;
DROP TABLE IF EXISTS public.employees;
DROP TABLE IF EXISTS public.employee_permissions;
DROP SEQUENCE IF EXISTS public.currencies_id_seq;
DROP TABLE IF EXISTS public.currencies;
DROP SEQUENCE IF EXISTS public.companies_id_seq;
DROP TABLE IF EXISTS public.companies;
DROP SEQUENCE IF EXISTS public.clients_id_seq;
DROP TABLE IF EXISTS public.clients;
DROP SEQUENCE IF EXISTS public.cards_id_seq;
DROP TABLE IF EXISTS public.cards;
DROP SEQUENCE IF EXISTS public.card_requests_id_seq;
DROP TABLE IF EXISTS public.card_requests;
DROP TABLE IF EXISTS public.backup_codes;
DROP SEQUENCE IF EXISTS public.authorized_party_id_seq;
DROP TABLE IF EXISTS public.authorized_party;
DROP SEQUENCE IF EXISTS public.activity_codes_id_seq;
DROP TABLE IF EXISTS public.activity_codes;
DROP SEQUENCE IF EXISTS public.accounts_id_seq;
DROP TABLE IF EXISTS public.accounts;
DROP FUNCTION IF EXISTS public.notify_permission_change();
DROP FUNCTION IF EXISTS public.notify_employee_status_change();
DROP TYPE IF EXISTS public.owner_type;
DROP TYPE IF EXISTS public.loan_type;
DROP TYPE IF EXISTS public.loan_status;
DROP TYPE IF EXISTS public.loan_request_status;
DROP TYPE IF EXISTS public.interest_rate_type;
DROP TYPE IF EXISTS public.installment_status;
DROP TYPE IF EXISTS public.employment_status;
DROP TYPE IF EXISTS public.card_type;
DROP TYPE IF EXISTS public.card_status;
DROP TYPE IF EXISTS public.card_brand;
DROP TYPE IF EXISTS public.account_type;
-- *not* dropping schema, since initdb creates it
--
-- Name: public; Type: SCHEMA; Schema: -; Owner: -
--

-- *not* creating schema, since initdb creates it


--
-- Name: SCHEMA public; Type: COMMENT; Schema: -; Owner: -
--

COMMENT ON SCHEMA public IS '';


--
-- Name: account_type; Type: TYPE; Schema: public; Owner: -
--

CREATE TYPE public.account_type AS ENUM (
    'checking',
    'foreign'
);


--
-- Name: card_brand; Type: TYPE; Schema: public; Owner: -
--

CREATE TYPE public.card_brand AS ENUM (
    'visa',
    'mastercard',
    'amex',
    'dinacard'
);


--
-- Name: card_status; Type: TYPE; Schema: public; Owner: -
--

CREATE TYPE public.card_status AS ENUM (
    'active',
    'blocked',
    'deactivated'
);


--
-- Name: card_type; Type: TYPE; Schema: public; Owner: -
--

CREATE TYPE public.card_type AS ENUM (
    'debit',
    'credit'
);


--
-- Name: employment_status; Type: TYPE; Schema: public; Owner: -
--

CREATE TYPE public.employment_status AS ENUM (
    'full_time',
    'temporary',
    'unemployed'
);


--
-- Name: installment_status; Type: TYPE; Schema: public; Owner: -
--

CREATE TYPE public.installment_status AS ENUM (
    'paid',
    'due',
    'late'
);


--
-- Name: interest_rate_type; Type: TYPE; Schema: public; Owner: -
--

CREATE TYPE public.interest_rate_type AS ENUM (
    'fixed',
    'variable'
);


--
-- Name: loan_request_status; Type: TYPE; Schema: public; Owner: -
--

CREATE TYPE public.loan_request_status AS ENUM (
    'pending',
    'approved',
    'rejected'
);


--
-- Name: loan_status; Type: TYPE; Schema: public; Owner: -
--

CREATE TYPE public.loan_status AS ENUM (
    'approved',
    'rejected',
    'paid',
    'late'
);


--
-- Name: loan_type; Type: TYPE; Schema: public; Owner: -
--

CREATE TYPE public.loan_type AS ENUM (
    'cash',
    'mortgage',
    'car',
    'refinancing',
    'student'
);


--
-- Name: owner_type; Type: TYPE; Schema: public; Owner: -
--

CREATE TYPE public.owner_type AS ENUM (
    'personal',
    'business'
);


--
-- Name: notify_employee_status_change(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.notify_employee_status_change() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF OLD.active IS DISTINCT FROM NEW.active THEN
        PERFORM pg_notify('permission_change', NEW.email);
    END IF;
    RETURN NEW;
END;
$$;


--
-- Name: notify_permission_change(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.notify_permission_change() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    emp_email TEXT;
BEGIN
    SELECT email INTO emp_email FROM employees
    WHERE id = COALESCE(NEW.employee_id, OLD.employee_id);

    IF emp_email IS NOT NULL THEN
        PERFORM pg_notify('permission_change', emp_email);
    END IF;
    RETURN COALESCE(NEW, OLD);
END;
$$;


SET default_tablespace = '';

SET default_table_access_method = heap;

--
-- Name: accounts; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.accounts (
    id bigint NOT NULL,
    number character varying(20) NOT NULL,
    name character varying(127) NOT NULL,
    owner bigint NOT NULL,
    balance numeric(20,2) DEFAULT 0 NOT NULL,
    created_by bigint,
    created_at date DEFAULT CURRENT_DATE NOT NULL,
    valid_until date NOT NULL,
    currency character varying(8) NOT NULL,
    active boolean DEFAULT false NOT NULL,
    owner_type public.owner_type NOT NULL,
    account_type public.account_type NOT NULL,
    maintainance_cost numeric(20,2) NOT NULL,
    daily_limit numeric(20,2),
    monthly_limit numeric(20,2),
    daily_expenditure numeric(20,2),
    monthly_expenditure numeric(20,2)
);


--
-- Name: accounts_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.accounts_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: accounts_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.accounts_id_seq OWNED BY public.accounts.id;


--
-- Name: activity_codes; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.activity_codes (
    id bigint NOT NULL,
    code character varying(7) NOT NULL,
    sector character varying(127) NOT NULL,
    branch character varying(255) NOT NULL
);


--
-- Name: activity_codes_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.activity_codes_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: activity_codes_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.activity_codes_id_seq OWNED BY public.activity_codes.id;


--
-- Name: authorized_party; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.authorized_party (
    id bigint NOT NULL,
    name character varying(63) NOT NULL,
    last_name character varying(63) NOT NULL,
    date_of_birth date NOT NULL,
    gender character varying(7) NOT NULL,
    email character varying(127) NOT NULL,
    phone_number character varying(15) NOT NULL,
    address character varying(255) NOT NULL
);


--
-- Name: authorized_party_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.authorized_party_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: authorized_party_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.authorized_party_id_seq OWNED BY public.authorized_party.id;


--
-- Name: backup_codes; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.backup_codes (
    client_id bigint,
    token character varying(6) NOT NULL,
    used boolean DEFAULT false NOT NULL
);


--
-- Name: card_requests; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.card_requests (
    id bigint NOT NULL,
    account_number character varying(20),
    type public.card_type DEFAULT 'debit'::public.card_type NOT NULL,
    brand public.card_brand NOT NULL,
    token character varying(255) NOT NULL,
    expiration_date date NOT NULL,
    complete boolean DEFAULT false NOT NULL,
    email character varying(255) NOT NULL
);


--
-- Name: card_requests_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.card_requests_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: card_requests_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.card_requests_id_seq OWNED BY public.card_requests.id;


--
-- Name: cards; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.cards (
    id bigint NOT NULL,
    number character varying(20) NOT NULL,
    type public.card_type DEFAULT 'debit'::public.card_type NOT NULL,
    brand public.card_brand NOT NULL,
    creation_date date DEFAULT CURRENT_DATE NOT NULL,
    valid_until date NOT NULL,
    account_number character varying(20),
    cvv character varying(7) NOT NULL,
    card_limit numeric(20,2),
    status public.card_status DEFAULT 'active'::public.card_status NOT NULL
);


--
-- Name: cards_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.cards_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: cards_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.cards_id_seq OWNED BY public.cards.id;


--
-- Name: clients; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.clients (
    id bigint NOT NULL,
    first_name character varying(100) NOT NULL,
    last_name character varying(100) NOT NULL,
    date_of_birth date NOT NULL,
    gender character varying(1) NOT NULL,
    email character varying(255) NOT NULL,
    phone_number character varying(20) NOT NULL,
    address character varying(255) NOT NULL,
    password bytea NOT NULL,
    salt_password bytea NOT NULL,
    created_at timestamp without time zone DEFAULT now() NOT NULL,
    updated_at timestamp without time zone DEFAULT now() NOT NULL
);


--
-- Name: clients_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.clients_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: clients_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.clients_id_seq OWNED BY public.clients.id;


--
-- Name: companies; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.companies (
    id bigint NOT NULL,
    registered_id bigint NOT NULL,
    name character varying(127) NOT NULL,
    tax_code bigint NOT NULL,
    activity_code_id bigint,
    address character varying(255) NOT NULL,
    owner_id bigint NOT NULL
);


--
-- Name: companies_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.companies_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: companies_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.companies_id_seq OWNED BY public.companies.id;


--
-- Name: currencies; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.currencies (
    id bigint NOT NULL,
    label character varying(8) NOT NULL,
    name character varying(64) NOT NULL,
    symbol character varying(8) NOT NULL,
    countries text NOT NULL,
    description character varying(1023) NOT NULL,
    active boolean DEFAULT true NOT NULL
);


--
-- Name: currencies_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.currencies_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: currencies_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.currencies_id_seq OWNED BY public.currencies.id;


--
-- Name: employee_permissions; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.employee_permissions (
    employee_id bigint NOT NULL,
    permission_id bigint NOT NULL
);


--
-- Name: employees; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.employees (
    id bigint NOT NULL,
    first_name character varying(100) NOT NULL,
    last_name character varying(100) NOT NULL,
    date_of_birth date NOT NULL,
    gender character varying(1) NOT NULL,
    email character varying(255) NOT NULL,
    phone_number character varying(20) NOT NULL,
    address character varying(255) NOT NULL,
    username character varying(100) NOT NULL,
    password bytea NOT NULL,
    salt_password bytea NOT NULL,
    "position" character varying(100) NOT NULL,
    department character varying(100) NOT NULL,
    active boolean DEFAULT true NOT NULL,
    created_at timestamp without time zone DEFAULT now() NOT NULL,
    updated_at timestamp without time zone DEFAULT now() NOT NULL
);


--
-- Name: employees_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.employees_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: employees_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.employees_id_seq OWNED BY public.employees.id;


--
-- Name: exchange_rates; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.exchange_rates (
    currency_code character varying(3) NOT NULL,
    rate_to_rsd numeric(20,6) NOT NULL,
    updated_at timestamp without time zone DEFAULT now() NOT NULL,
    valid_until timestamp without time zone DEFAULT now() NOT NULL
);


--
-- Name: loan_installment; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.loan_installment (
    id bigint NOT NULL,
    loan_id bigint,
    installment_amount numeric(20,2) NOT NULL,
    interest_rate numeric(5,2) NOT NULL,
    currency_id bigint NOT NULL,
    due_date date NOT NULL,
    paid_date date NOT NULL,
    status public.installment_status DEFAULT 'due'::public.installment_status NOT NULL
);


--
-- Name: loan_installment_currency_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.loan_installment_currency_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: loan_installment_currency_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.loan_installment_currency_id_seq OWNED BY public.loan_installment.currency_id;


--
-- Name: loan_installment_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.loan_installment_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: loan_installment_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.loan_installment_id_seq OWNED BY public.loan_installment.id;


--
-- Name: loan_request; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.loan_request (
    id bigint NOT NULL,
    type public.loan_type NOT NULL,
    currency_id bigint,
    amount numeric(20,2) NOT NULL,
    repayment_period bigint NOT NULL,
    account_id bigint,
    status public.loan_request_status DEFAULT 'pending'::public.loan_request_status NOT NULL,
    submission_date timestamp without time zone DEFAULT now() NOT NULL,
    purpose character varying(255),
    salary numeric(20,2),
    employment_status public.employment_status,
    employment_period bigint,
    phone_number character varying(32),
    interest_rate_type public.interest_rate_type DEFAULT 'fixed'::public.interest_rate_type NOT NULL
);


--
-- Name: loan_request_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.loan_request_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: loan_request_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.loan_request_id_seq OWNED BY public.loan_request.id;


--
-- Name: loans; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.loans (
    id bigint NOT NULL,
    account_id bigint,
    amount numeric(20,2) NOT NULL,
    currency_id bigint NOT NULL,
    installments bigint NOT NULL,
    nominal_rate numeric(5,2) NOT NULL,
    interest_rate numeric(5,2) NOT NULL,
    date_signed date NOT NULL,
    date_end date NOT NULL,
    monthly_payment numeric(20,2) NOT NULL,
    next_payment_due date NOT NULL,
    remaining_debt numeric(20,2) NOT NULL,
    type public.loan_type NOT NULL,
    loan_status public.loan_status DEFAULT 'approved'::public.loan_status NOT NULL,
    interest_rate_type public.interest_rate_type NOT NULL
);


--
-- Name: loans_currency_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.loans_currency_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: loans_currency_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.loans_currency_id_seq OWNED BY public.loans.currency_id;


--
-- Name: loans_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.loans_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: loans_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.loans_id_seq OWNED BY public.loans.id;


--
-- Name: password_action_tokens; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.password_action_tokens (
    email character varying(255) NOT NULL,
    action_type character varying(20) NOT NULL,
    hashed_token bytea NOT NULL,
    valid_until timestamp without time zone NOT NULL,
    used boolean DEFAULT false NOT NULL,
    created_at timestamp without time zone DEFAULT now() NOT NULL,
    used_at timestamp without time zone,
    CONSTRAINT password_action_tokens_action_type_check CHECK (((action_type)::text = ANY ((ARRAY['reset'::character varying, 'initial_set'::character varying, 'totp_disable'::character varying])::text[])))
);


--
-- Name: payment_codes; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.payment_codes (
    code bigint NOT NULL,
    description character varying(255) NOT NULL
);


--
-- Name: payment_recipients; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.payment_recipients (
    id bigint NOT NULL,
    client_id bigint NOT NULL,
    name character varying(127) NOT NULL,
    account_number character varying(20) NOT NULL,
    created_at timestamp without time zone DEFAULT now() NOT NULL,
    updated_at timestamp without time zone DEFAULT now() NOT NULL
);


--
-- Name: payment_recipients_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.payment_recipients_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: payment_recipients_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.payment_recipients_id_seq OWNED BY public.payment_recipients.id;


--
-- Name: payments; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.payments (
    transaction_id bigint NOT NULL,
    from_account character varying(20),
    to_account character varying(20),
    start_amount numeric(20,2) NOT NULL,
    end_amount numeric(20,2) NOT NULL,
    commission numeric(20,2) NOT NULL,
    status character varying(20) DEFAULT 'realized'::character varying NOT NULL,
    recipient_id bigint,
    transcaction_code integer NOT NULL,
    call_number character varying(31) NOT NULL,
    reason character varying(255) NOT NULL,
    "timestamp" timestamp without time zone DEFAULT now() NOT NULL,
    CONSTRAINT payments_status_check CHECK (((status)::text = ANY ((ARRAY['realized'::character varying, 'rejected'::character varying, 'pending'::character varying])::text[])))
);


--
-- Name: payments_transaction_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.payments_transaction_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: payments_transaction_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.payments_transaction_id_seq OWNED BY public.payments.transaction_id;


--
-- Name: permissions; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.permissions (
    id bigint NOT NULL,
    name character varying(100) NOT NULL
);


--
-- Name: permissions_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.permissions_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: permissions_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.permissions_id_seq OWNED BY public.permissions.id;


--
-- Name: refresh_tokens; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.refresh_tokens (
    session_id character varying(64) NOT NULL,
    email character varying(255) NOT NULL,
    hashed_token bytea NOT NULL,
    valid_until timestamp without time zone NOT NULL,
    revoked boolean DEFAULT false NOT NULL
);


--
-- Name: transaction_verification_codes; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.transaction_verification_codes (
    client_id bigint NOT NULL,
    code character varying(6) NOT NULL,
    valid_until timestamp without time zone NOT NULL,
    failed_attempts integer DEFAULT 0 NOT NULL,
    max_attempts integer DEFAULT 3 NOT NULL,
    used boolean DEFAULT false NOT NULL,
    canceled boolean DEFAULT false NOT NULL,
    created_at timestamp without time zone DEFAULT now() NOT NULL
);


--
-- Name: transfers; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.transfers (
    transaction_id bigint NOT NULL,
    from_account character varying(20),
    to_account character varying(20),
    start_amount numeric(20,2) NOT NULL,
    end_amount numeric(20,2) NOT NULL,
    start_currency_id bigint,
    exchange_rate numeric(20,2),
    commission numeric(20,2) NOT NULL,
    status character varying(20) DEFAULT 'pending'::character varying NOT NULL,
    "timestamp" timestamp without time zone DEFAULT now() NOT NULL,
    CONSTRAINT transfers_status_check CHECK (((status)::text = ANY ((ARRAY['pending'::character varying, 'completed'::character varying, 'rejected'::character varying])::text[])))
);


--
-- Name: transfers_transaction_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.transfers_transaction_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: transfers_transaction_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.transfers_transaction_id_seq OWNED BY public.transfers.transaction_id;


--
-- Name: verification_codes; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.verification_codes (
    client_id bigint NOT NULL,
    enabled boolean DEFAULT false NOT NULL,
    secret character varying(32),
    temp_secret character varying(32),
    temp_created_at timestamp without time zone DEFAULT now() NOT NULL
);


--
-- Name: accounts id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.accounts ALTER COLUMN id SET DEFAULT nextval('public.accounts_id_seq'::regclass);


--
-- Name: activity_codes id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.activity_codes ALTER COLUMN id SET DEFAULT nextval('public.activity_codes_id_seq'::regclass);


--
-- Name: authorized_party id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.authorized_party ALTER COLUMN id SET DEFAULT nextval('public.authorized_party_id_seq'::regclass);


--
-- Name: card_requests id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.card_requests ALTER COLUMN id SET DEFAULT nextval('public.card_requests_id_seq'::regclass);


--
-- Name: cards id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.cards ALTER COLUMN id SET DEFAULT nextval('public.cards_id_seq'::regclass);


--
-- Name: clients id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.clients ALTER COLUMN id SET DEFAULT nextval('public.clients_id_seq'::regclass);


--
-- Name: companies id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.companies ALTER COLUMN id SET DEFAULT nextval('public.companies_id_seq'::regclass);


--
-- Name: currencies id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.currencies ALTER COLUMN id SET DEFAULT nextval('public.currencies_id_seq'::regclass);


--
-- Name: employees id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.employees ALTER COLUMN id SET DEFAULT nextval('public.employees_id_seq'::regclass);


--
-- Name: loan_installment id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.loan_installment ALTER COLUMN id SET DEFAULT nextval('public.loan_installment_id_seq'::regclass);


--
-- Name: loan_installment currency_id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.loan_installment ALTER COLUMN currency_id SET DEFAULT nextval('public.loan_installment_currency_id_seq'::regclass);


--
-- Name: loan_request id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.loan_request ALTER COLUMN id SET DEFAULT nextval('public.loan_request_id_seq'::regclass);


--
-- Name: loans id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.loans ALTER COLUMN id SET DEFAULT nextval('public.loans_id_seq'::regclass);


--
-- Name: loans currency_id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.loans ALTER COLUMN currency_id SET DEFAULT nextval('public.loans_currency_id_seq'::regclass);


--
-- Name: payment_recipients id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.payment_recipients ALTER COLUMN id SET DEFAULT nextval('public.payment_recipients_id_seq'::regclass);


--
-- Name: payments transaction_id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.payments ALTER COLUMN transaction_id SET DEFAULT nextval('public.payments_transaction_id_seq'::regclass);


--
-- Name: permissions id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.permissions ALTER COLUMN id SET DEFAULT nextval('public.permissions_id_seq'::regclass);


--
-- Name: transfers transaction_id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.transfers ALTER COLUMN transaction_id SET DEFAULT nextval('public.transfers_transaction_id_seq'::regclass);


--
-- Data for Name: accounts; Type: TABLE DATA; Schema: public; Owner: -
--

COPY public.accounts (id, number, name, owner, balance, created_by, created_at, valid_until, currency, active, owner_type, account_type, maintainance_cost, daily_limit, monthly_limit, daily_expenditure, monthly_expenditure) FROM stdin;
3	333000100000000320	Banka 3 - CHF	3	2000000.00	1	2026-04-09	2099-12-31	CHF	t	business	foreign	0.00	\N	\N	0.00	0.00
5	333000100000000520	Banka 3 - GBP	3	1500000.00	1	2026-04-09	2099-12-31	GBP	t	business	foreign	0.00	\N	\N	0.00	0.00
6	333000100000000620	Banka 3 - JPY	3	100000000.00	1	2026-04-09	2099-12-31	JPY	t	business	foreign	0.00	\N	\N	0.00	0.00
7	333000100000000720	Banka 3 - CAD	3	1000000.00	1	2026-04-09	2099-12-31	CAD	t	business	foreign	0.00	\N	\N	0.00	0.00
8	333000100000000820	Banka 3 - AUD	3	800000.00	1	2026-04-09	2099-12-31	AUD	t	business	foreign	0.00	\N	\N	0.00	0.00
10	333000112345678920	Petar devizni EUR	1	50000.00	1	2026-04-09	2029-12-31	EUR	t	personal	foreign	0.00	500000.00	2000000.00	0.00	0.00
11	333000198765432110	Marko tekuci	4	8500000.00	1	2026-04-09	2029-12-31	RSD	t	personal	checking	25500.00	25000000.00	100000000.00	0.00	0.00
12	333000198765432120	Marko devizni USD	4	20000.00	1	2026-04-09	2029-12-31	USD	t	personal	foreign	0.00	500000.00	2000000.00	0.00	0.00
13	333000155555555510	Jovana tekuci	5	22000000.00	1	2026-04-09	2029-12-31	RSD	t	personal	checking	25500.00	25000000.00	100000000.00	0.00	0.00
14	333000155555555520	Jovana devizni EUR	5	100000.00	1	2026-04-09	2029-12-31	EUR	t	personal	foreign	0.00	500000.00	2000000.00	0.00	0.00
15	333000155555555620	Jovana devizni CHF	5	30000.00	1	2026-04-09	2029-12-31	CHF	t	personal	foreign	0.00	500000.00	2000000.00	0.00	0.00
2	333000100000000220	Banka 3 - EUR	3	5001857.54	1	2026-04-09	2099-12-31	EUR	t	business	foreign	0.00	\N	\N	0.00	0.00
4	333000100000000420	Banka 3 - USD	3	3007905.24	1	2026-04-09	2099-12-31	USD	t	business	foreign	0.00	\N	\N	0.00	0.00
20	333000199999999130	Marko devizni USD 1	6	122997094.76	1	2026-04-09	2029-12-31	USD	t	personal	foreign	0.00	500000.00	2000000.00	10384.66	10384.66
16	333000199999999110	Marko tekuci 1	6	8913000.09	1	2026-04-09	2029-12-31	RSD	t	personal	checking	25500.00	25000000.00	100000000.00	1189042.67	1189042.67
17	333000199999999210	Marko tekuci 2	6	1778037.90	1	2026-04-09	2029-12-31	RSD	t	personal	checking	25500.00	25000000.00	100000000.00	77.00	77.00
9	333000112345678910	Petar tekuci	1	16162000.00	1	2026-04-09	2029-12-31	RSD	t	personal	checking	25500.00	25000000.00	100000000.00	0.00	0.00
21	333000199999999230	Marko devizni USD 2	6	29995000.00	1	2026-04-09	2029-12-31	USD	t	personal	foreign	0.00	500000.00	2000000.00	5284.66	5284.66
1	333000100000000110	Banka 3 - RSD	3	998924739.01	1	2026-04-09	2099-12-31	RSD	t	business	checking	0.00	\N	\N	0.00	0.00
18	333000199999999120	Marko devizni EUR 1	6	1990142.79	1	2026-04-09	2029-12-31	EUR	t	personal	foreign	0.00	500000.00	2000000.00	11870.10	11870.10
19	333000199999999220	Marko devizni EUR 2	6	80007999.67	1	2026-04-09	2029-12-31	EUR	t	personal	foreign	0.00	500000.00	2000000.00	1992.33	1992.33
\.


--
-- Data for Name: activity_codes; Type: TABLE DATA; Schema: public; Owner: -
--

COPY public.activity_codes (id, code, sector, branch) FROM stdin;
1	64.19	Financial services	Banking
2	62.01	IT	Computer programming activities
3	47.11	Retail	Retail sale in non-specialized stores
\.


--
-- Data for Name: authorized_party; Type: TABLE DATA; Schema: public; Owner: -
--

COPY public.authorized_party (id, name, last_name, date_of_birth, gender, email, phone_number, address) FROM stdin;
1	Ana	Petrovic	1992-07-12	F	ana.petrovic@example.com	+381641111111	Nemanjina 5
\.


--
-- Data for Name: backup_codes; Type: TABLE DATA; Schema: public; Owner: -
--

COPY public.backup_codes (client_id, token, used) FROM stdin;
\.


--
-- Data for Name: card_requests; Type: TABLE DATA; Schema: public; Owner: -
--

COPY public.card_requests (id, account_number, type, brand, token, expiration_date, complete, email) FROM stdin;
1	333000199999999130	debit	visa	tkn-1775860175069252166-20	2026-04-11	f	marko.petrovic@gmail.com
2	333000199999999110	debit	visa	tkn-1775860229121588404-16	2026-04-11	f	marko.petrovic@gmail.com
3	333000199999999130	debit	visa	tkn-1775860353160521470-20	2026-04-11	f	marko.petrovic@gmail.com
4	333000199999999130	debit	visa	tkn-1775860369715870499-20	2026-04-11	f	marko.petrovic@gmail.com
5	333000199999999130	debit	visa	tkn-1775860667937849883-20	2026-04-11	f	marko.petrovic@gmail.com
6	333000199999999110	debit	visa	tkn-1775860741285696988-16	2026-04-11	f	marko.petrovic@gmail.com
7	333000199999999130	debit	visa	tkn-1775861058253623559-20	2026-04-11	f	marko.petrovic@gmail.com
8	333000199999999130	credit	visa	tkn-1775861085146992019-20	2026-04-11	f	marko.petrovic@gmail.com
9	333000199999999130	debit	visa	tkn-1775861507387321559-20	2026-04-11	f	marko.petrovic@gmail.com
10	333000199999999130	debit	visa	tkn-1775861930738501035-20	2026-04-11	f	marko.petrovic@gmail.com
11	333000199999999130	debit	visa	tkn-1775862093589519331-20	2026-04-11	f	marko.petrovic@gmail.com
12	333000199999999130	debit	visa	tkn-1775862122045287865-20	2026-04-11	f	marko.petrovic@gmail.com
13	333000199999999110	debit	mastercard	tkn-1775862136263381287-16	2026-04-11	f	marko.petrovic@gmail.com
14	333000199999999130	credit	visa	tkn-1775862202442981904-20	2026-04-11	f	marko.petrovic@gmail.com
15	333000199999999130	debit	visa	tkn-1775862357815043761-20	2026-04-11	f	marko.petrovic@gmail.com
16	333000199999999220	debit	mastercard	tkn-1775862371144194987-19	2026-04-11	f	marko.petrovic@gmail.com
\.


--
-- Data for Name: cards; Type: TABLE DATA; Schema: public; Owner: -
--

COPY public.cards (id, number, type, brand, creation_date, valid_until, account_number, cvv, card_limit, status) FROM stdin;
1	4333001234567890	debit	visa	2026-04-09	2030-06-30	333000112345678910	123	5000000.00	active
2	5333009876543210	debit	mastercard	2026-04-09	2030-06-30	333000198765432110	456	5000000.00	active
3	4333005555555555	debit	visa	2026-04-09	2030-06-30	333000155555555510	789	5000000.00	active
4	4333001999999999	debit	visa	2026-04-11	2030-06-30	333000199999999110	842	5000000.00	active
\.


--
-- Data for Name: clients; Type: TABLE DATA; Schema: public; Owner: -
--

COPY public.clients (id, first_name, last_name, date_of_birth, gender, email, phone_number, address, password, salt_password, created_at, updated_at) FROM stdin;
1	Petar	Petrovic	1990-05-20	M	petar@primer.raf	+381645555555	Njegoseva 25	\\xa514f71947f5447cdfc2845f40d020cea4146ba28e84cb1a82662a6286f8228d	\\x11223344556677889900aabbccddeeff	2026-04-09 10:45:45.670548	2026-04-09 10:45:45.670548
2	Aleksa	Nikolic	1983-04-13	M	aleksa@primer.raf	+38161238472345	Novi Beograd 12	\\x5f8c3b0b8c4c6c5f9d7a2a5f3d7c2d2e6a0c9c1b4b9f2e3a6d8e1f0a2b3c4d5e	\\x9f3a1c7e5b2d4a8c6e1f0923ab47cd11	2026-04-09 10:45:45.673493	2026-04-09 10:45:45.673493
3	Banka	Tri	2000-01-01	M	system@banka3.rs	+381600000001	Bulevar Kralja Aleksandra 73	\\x0000000000000000000000000000000000000000000000000000000000000000	\\x00000000000000000000000000000000	2026-04-09 10:45:45.675358	2026-04-09 10:45:45.675358
4	Marko	Markovic	1985-11-15	M	marko@primer.raf	+381641234567	Knez Mihailova 10	\\xa514f71947f5447cdfc2845f40d020cea4146ba28e84cb1a82662a6286f8228d	\\x11223344556677889900aabbccddeeff	2026-04-09 10:45:45.677586	2026-04-09 10:45:45.677586
5	Jovana	Jovanovic	1995-03-08	F	jovana@primer.raf	+381649876543	Cara Dusana 44	\\xa514f71947f5447cdfc2845f40d020cea4146ba28e84cb1a82662a6286f8228d	\\x11223344556677889900aabbccddeeff	2026-04-09 10:45:45.679125	2026-04-09 10:45:45.679125
6	Marko	Petrovic	1990-01-01	M	marko.petrovic@gmail.com	+381641234567	Knez Mihailova 77 Beograd	\\xa7368ef98abae0935b8e62721ac0db8ff50a07a2c8e60bde9a32175354632bd9	\\x11223344556677889900aabbccddeeff	2026-04-09 10:50:25.290489	2026-04-09 10:50:25.290489
\.


--
-- Data for Name: companies; Type: TABLE DATA; Schema: public; Owner: -
--

COPY public.companies (id, registered_id, name, tax_code, activity_code_id, address, owner_id) FROM stdin;
1	33300001	Banka 3 AD Beograd	100000003	1	Bulevar Kralja Aleksandra 73	3
2	10203040	TechSerbia DOO	200000001	2	Bulevar Oslobodjenja 15	4
\.


--
-- Data for Name: currencies; Type: TABLE DATA; Schema: public; Owner: -
--

COPY public.currencies (id, label, name, symbol, countries, description, active) FROM stdin;
1	RSD	Serbian Dinar	din.	Serbia	The Serbian dinar (symbol: din.; code: RSD) is the official currency of Serbia. One dinar is subdivided into 100 para.	t
2	EUR	Euro	?	Austria, Belgium, Bulgaria, Croatia, Cyprus, Estonia, Finland, France, Germany, Greece, Ireland, Italy, Latvia, Lithuania, Luxembourg, Malta, Netherlands, Portugal, Slovakia, Slovenia, Spain	The euro (symbol: ?; currency code: EUR) is the official currency of 21 of the 27 member states of the European Union. This group of states is officially known as the euro area or, more commonly, the eurozone. The euro is divided into 100 euro cents.	t
3	CHF	Swiss Franc	CHF	Switzerland, Liechtenstein	The Swiss franc (symbol: CHF) is the currency and legal tender of Switzerland and Liechtenstein. It is also legal tender in the Italian exclave of Campione d'Italia.	t
4	USD	US Dollar	$	United States, Puerto Rico, Ecuador, El Salvador, Zimbabwe	The United States dollar (symbol: $; code: USD) is the official currency of the United States and several other countries. It is divided into 100 cents.	t
5	GBP	British Pound	?	United Kingdom, Jersey, Guernsey, Isle of Man	The pound sterling (symbol: ?; code: GBP) is the official currency of the United Kingdom and the Crown Dependencies. It is subdivided into 100 pence.	t
6	JPY	Japanese Yen	?	Japan	The Japanese yen (symbol: ?; code: JPY) is the official currency of Japan. It is the third-most traded currency in the foreign exchange market after the US dollar and the euro.	t
7	CAD	Canadian Dollar	C$	Canada	The Canadian dollar (symbol: C$; code: CAD) is the currency of Canada. It is abbreviated with the dollar sign $, or sometimes CA$ to distinguish it from other dollar-denominated currencies.	t
8	AUD	Australian Dollar	A$	Australia, Christmas Island, Cocos Islands, Norfolk Island	The Australian dollar (symbol: A$; code: AUD) is the official currency and legal tender of Australia, including all of its external territories.	t
\.


--
-- Data for Name: employee_permissions; Type: TABLE DATA; Schema: public; Owner: -
--

COPY public.employee_permissions (employee_id, permission_id) FROM stdin;
1	1
2	2
2	3
2	4
2	6
2	7
2	8
2	9
2	10
3	3
\.


--
-- Data for Name: employees; Type: TABLE DATA; Schema: public; Owner: -
--

COPY public.employees (id, first_name, last_name, date_of_birth, gender, email, phone_number, address, username, password, salt_password, "position", department, active, created_at, updated_at) FROM stdin;
1	Admin	Admin	1990-01-01	M	admin@banka.raf	+381600000000	N/A	admin	\\x78db8c5a70624a77ff540ee38898086ab4db699e8905399b8a84c485cd7c4953	\\xf5e2740f7afc0e0dd44968b7364fc102	Administrator	IT	t	2026-04-09 10:45:45.647382	2026-04-09 10:45:45.647382
2	Full	Access	1990-01-01	M	full_emp@banka.raf	+381649990001	Test Adresa 1	fullemp	\\xa514f71947f5447cdfc2845f40d020cea4146ba28e84cb1a82662a6286f8228d	\\x11223344556677889900aabbccddeeff	Manager	Operations	t	2026-04-09 10:45:45.658713	2026-04-09 10:45:45.658713
3	Limited	Access	1990-01-01	F	limited_emp@banka.raf	+381649990002	Test Adresa 2	limitedemp	\\xa514f71947f5447cdfc2845f40d020cea4146ba28e84cb1a82662a6286f8228d	\\x11223344556677889900aabbccddeeff	Viewer	Support	t	2026-04-09 10:45:45.665187	2026-04-09 10:45:45.665187
\.


--
-- Data for Name: exchange_rates; Type: TABLE DATA; Schema: public; Owner: -
--

COPY public.exchange_rates (currency_code, rate_to_rsd, updated_at, valid_until) FROM stdin;
EUR	117.150000	2026-04-09 10:45:45.683905	2026-04-10 10:45:45.683905
CHF	120.450000	2026-04-09 10:45:45.683905	2026-04-10 10:45:45.683905
USD	108.500000	2026-04-09 10:45:45.683905	2026-04-10 10:45:45.683905
GBP	137.200000	2026-04-09 10:45:45.683905	2026-04-10 10:45:45.683905
JPY	0.720000	2026-04-09 10:45:45.683905	2026-04-10 10:45:45.683905
CAD	79.800000	2026-04-09 10:45:45.683905	2026-04-10 10:45:45.683905
AUD	70.250000	2026-04-09 10:45:45.683905	2026-04-10 10:45:45.683905
\.


--
-- Data for Name: loan_installment; Type: TABLE DATA; Schema: public; Owner: -
--

COPY public.loan_installment (id, loan_id, installment_amount, interest_rate, currency_id, due_date, paid_date, status) FROM stdin;
1	1	912000.00	6.12	1	2025-02-15	2025-02-14	paid
2	1	912000.00	6.12	1	2025-03-15	2025-03-15	paid
3	1	912000.00	6.12	1	2025-04-15	2025-04-14	paid
4	1	912000.00	6.12	1	2026-04-15	2026-04-15	due
6	2	91429.02	6.12	1	2026-05-11	2026-05-11	due
\.


--
-- Data for Name: loan_request; Type: TABLE DATA; Schema: public; Owner: -
--

COPY public.loan_request (id, type, currency_id, amount, repayment_period, account_id, status, submission_date, purpose, salary, employment_status, employment_period, phone_number, interest_rate_type) FROM stdin;
1	cash	1	100000.00	60	16	pending	2026-04-10 22:45:54.633085	Trosak	1000000.00	full_time	7	38163777777	fixed
2	cash	1	1000000.00	60	16	pending	2026-04-10 23:04:57.685037	F	5555.00	full_time	7	85555	fixed
3	cash	1	55555.00	60	17	pending	2026-04-10 23:05:42.331007	Tffg	5555.00	full_time	6	665	fixed
\.


--
-- Data for Name: loans; Type: TABLE DATA; Schema: public; Owner: -
--

COPY public.loans (id, account_id, amount, currency_id, installments, nominal_rate, interest_rate, date_signed, date_end, monthly_payment, next_payment_due, remaining_debt, type, loan_status, interest_rate_type) FROM stdin;
1	9	30000000.00	1	36	5.75	6.12	2025-01-15	2028-01-15	912000.00	2026-04-15	21888000.00	cash	approved	fixed
2	16	3000000.00	1	36	5.75	6.12	2026-04-11	2029-04-11	91429.02	2026-05-11	3000000.00	cash	approved	fixed
\.


--
-- Data for Name: password_action_tokens; Type: TABLE DATA; Schema: public; Owner: -
--

COPY public.password_action_tokens (email, action_type, hashed_token, valid_until, used, created_at, used_at) FROM stdin;
\.


--
-- Data for Name: payment_codes; Type: TABLE DATA; Schema: public; Owner: -
--

COPY public.payment_codes (code, description) FROM stdin;
120	Doznake po tekucem racunu - ostale doznake
220	Komunalne usluge
221	Elektricna energija
222	Gas
223	Vodovod i kanalizacija
240	Telekomunikacione usluge
253	Hartije od vrednosti
265	Placanje premije osiguranja
289	Ostale finansijske transakcije
290	Kupoprodajne transakcije
\.


--
-- Data for Name: payment_recipients; Type: TABLE DATA; Schema: public; Owner: -
--

COPY public.payment_recipients (id, client_id, name, account_number, created_at, updated_at) FROM stdin;
\.


--
-- Data for Name: payments; Type: TABLE DATA; Schema: public; Owner: -
--

COPY public.payments (transaction_id, from_account, to_account, start_amount, end_amount, commission, status, recipient_id, transcaction_code, call_number, reason, "timestamp") FROM stdin;
1	333000112345678910	333000198765432110	50000.00	50000.00	0.00	realized	4	289	00112233	Vracanje duga za veceru	2026-04-09 10:45:45.738322
2	333000198765432110	333000112345678910	25000.00	25000.00	0.00	realized	1	290	00445566	Kupovina laptopa	2026-04-09 10:45:45.741974
3	333000199999999130	333000112345678910	10000.00	1085000.00	100.00	realized	1	289	174528	EPS Beograd	2026-04-09 12:03:52.366021
4	333000199999999110	333000112345678910	7000.00	7000.00	0.00	realized	1	289	25885	EPS Beograd	2026-04-09 13:29:56.612702
5	333000199999999110	333000112345678910	10000.00	10000.00	0.00	realized	1	289	35265	Placam vodu	2026-04-09 13:38:12.077814
6	333000199999999110	333000112345678910	15000.00	15000.00	0.00	realized	1	289	547	Telenor 777	2026-04-09 13:42:18.674849
7	333000199999999110	333000112345678910	11111.11	11111.11	0.00	realized	1	289	35252	Vodovod Beograd	2026-04-09 13:47:15.262151
8	333000199999999110	333000112345678910	7777.00	7777.00	0.00	realized	1	289	7777	Telenor Srbija	2026-04-09 19:55:10.288323
9	333000199999999110	333000112345678910	111.89	111.89	0.00	realized	1	289	652	Svrha placanja	2026-04-09 19:59:42.799815
10	333000199999999110	333000112345678910	25000.01	25000.01	0.00	realized	1	289	11111111	La la la	2026-04-09 20:20:20.089553
11	333000199999999110	333000112345678910	999.99	999.99	0.00	realized	1	289		EPS Beograd	2026-04-09 20:21:14.869157
\.


--
-- Data for Name: permissions; Type: TABLE DATA; Schema: public; Owner: -
--

COPY public.permissions (id, name) FROM stdin;
1	admin
2	trade_stocks
3	view_stocks
4	manage_loans
5	manage_insurance
6	manage_employees
7	manage_clients
8	manage_accounts
9	manage_companies
10	manage_cards
\.


--
-- Data for Name: refresh_tokens; Type: TABLE DATA; Schema: public; Owner: -
--

COPY public.refresh_tokens (session_id, email, hashed_token, valid_until, revoked) FROM stdin;
286b69a3-34d1-43f4-91da-9a413ae2e4f0	admin@banka.raf	\\x5637e6b7179380a785d7ba450e069d4ce10b9878068261c76473ef8d9c682c1a	2026-04-16 10:47:56	f
39d5c113-2050-4e3c-ace9-1d4216c64887	admin@banka.raf	\\x0d35a31fd04abae751687b12ac08a98378c95a3e543b9204de290542b4a63ee3	2026-04-16 10:47:56	f
2889e2f5-d4c3-4905-83fb-44443b4d07e8	admin@banka.raf	\\x9898440845684ab5434b2eb3e72fc53b8f8b41e5ec434c68253f94a588ebd449	2026-04-16 10:47:56	f
ea8c8970-4ef9-4d25-b7fe-11c4a1a06b05	admin@banka.raf	\\x89e277f7d1538b9587e49c5deb6cc49a0dd9a286d697759f420a9e79e694c9ad	2026-04-16 10:48:09	f
5f83f4d0-0939-4f5b-b91d-c8b0f9739e7a	admin@banka.raf	\\x9123bc126bf3ee533b6ceb334cda871014ad82ca7842deac6eb7ced25eb73e18	2026-04-16 10:48:09	t
4ebffb26-d12b-4c51-91e5-1ebad28af4c5	admin@banka.raf	\\xee6665afa6a88adf59cde61fab4118844895bfe1270e21fe36684f7d491fc60c	2026-04-16 10:49:24	f
54fb3a62-cd08-42cf-be89-cd7b84cddba7	admin@banka.raf	\\xb294beba70566eaa07bba28f837ae8c1056b6e76c61c3cead56c54be1f688a16	2026-04-16 10:49:24	f
4f3f7977-5b90-49e1-a3e2-0e14826e3435	admin@banka.raf	\\x4e01cc9d578a527befeb67755a4252405836da31ea4c16a0598a1f86b298ce7e	2026-04-16 10:49:24	f
10ef6753-b54e-4828-b618-0da2b3b90df1	marko.petrovic@gmail.com	\\xab565555d04d34693c03a70c2bd172eddef93bb4b76a295116379fb97449b44f	2026-04-16 10:50:30	f
7ef7e6a3-6026-4c91-9fcf-3fef20e75514	marko.petrovic@gmail.com	\\x546139e1a698e2c631112a4d3b7ac95379758f16fa9a56426129f288eb1c54d9	2026-04-16 11:42:00	f
897a17b7-c494-4e14-bd8b-ae7d22301347	marko.petrovic@gmail.com	\\x9ece1b641fcb901e4b3a014eabe67a6c67143dab7f4d9034f41c32f19d79e78c	2026-04-18 16:46:24	f
7b40c3c1-b59d-4797-ba34-621f542eea37	marko.petrovic@gmail.com	\\x1f77773cd0849e9d92fef39baa440a0841ae2c4195968811075be6ed7b1583c4	2026-04-18 16:49:51	f
81e8b88e-8706-4930-a03b-e56d9cae6f4d	marko.petrovic@gmail.com	\\x3ba8621330041e686d812b232bebdfb10f9c3a187403f7c59f4b50d72cca1f25	2026-04-16 19:55:52	f
dd4523b6-1420-450b-a56a-a51a7c84997a	marko.petrovic@gmail.com	\\x62baca009561c072d56e9783f415ae6062105bb6ab425cced5b9e15ed11d8e31	2026-04-16 19:54:42	t
9c6fc1b5-7119-4729-97f1-0901fba1aeb5	marko.petrovic@gmail.com	\\xdc592088bfa1413ea56c5c5c9cd6c59fda327478db1f7d8d153749de77652d7c	2026-04-16 19:58:32	f
63101168-84eb-44e1-9c79-15038198ddd9	marko.petrovic@gmail.com	\\xc4e2c1fb8018b2fe30d71d260c18f75a2278ee35c145f4fcc2f9315c2fbefb7c	2026-04-16 20:07:13	f
643ff493-f96f-4e9c-b45e-6b74dfd23332	marko.petrovic@gmail.com	\\x9e1bdc01ce034dffd7a40168cfc759a568e6f9c03ee07ad6f9e6bd92c261d762	2026-04-16 20:10:32	f
24f83779-8b07-4c2d-b330-38f2b748b20a	marko.petrovic@gmail.com	\\x067d497f2c4f95a1d10a600096a78e333b6c7bbb2f9183f72bedc472c2a3414f	2026-04-16 20:12:22	f
53faa066-afae-4e1f-9313-e327d8cdd304	marko.petrovic@gmail.com	\\x6fc5ec7c1aae869f72a9ebb454194931446b4723672aba193a7da2deb0bd8101	2026-04-16 20:15:12	f
7da0a68c-e039-47b3-857d-e0d026692586	marko.petrovic@gmail.com	\\x2e3636257aa1467e3547ca50a29cd0dae14f829115c3862bf53b24d8e01504a0	2026-04-16 20:16:55	f
122fc976-9d4b-41ab-9653-9aad852acb99	marko.petrovic@gmail.com	\\x04d80f9c287784b2271fbb37b0f915b69ac283c6b2e89630dc4fadf3f44317c1	2026-04-16 20:17:41	t
18bd99d1-a006-45e2-80f7-8e7580559295	marko.petrovic@gmail.com	\\xd4b11fc863886609ec2891695d66a665c671b1e48e423c64066bc80bfd886a75	2026-04-16 20:22:36	f
02152e8c-e047-48b7-8246-94eb40ec6457	marko.petrovic@gmail.com	\\x0adfb870ef06ecf560cb16cade4112e39cc7efbf7501b8117c7b4ad3ba1707af	2026-04-16 20:31:04	f
f4513c32-9677-4125-ab72-6572e8539d9f	marko.petrovic@gmail.com	\\xbde845a828d2a74b6a4866b4ba87c87c44e7cfdc099798c61c3143264f6c95ed	2026-04-17 17:52:52	f
d417d469-bc12-437b-a382-f87c1897b118	marko.petrovic@gmail.com	\\x2addbe64e61e22d08259f83239c7c63e03302d2e1fc3b96ec834461a36f064ef	2026-04-17 18:09:18	f
c45d8449-8711-4764-97f8-81673a73d809	marko.petrovic@gmail.com	\\x0cafbe8fb899ac19e72764d0136da80f497d33da3306acc1327a9b9d74843ebd	2026-04-17 21:54:28	f
c76dfb0f-3734-4e35-9c5a-a7c8f940f885	marko.petrovic@gmail.com	\\x3e8ccaeab0df1317efc6e2c896356090302005d5868a1335cb512225ec51e8e3	2026-04-17 21:56:27	f
dd395280-0384-46b2-a5be-e0639f2c6393	marko.petrovic@gmail.com	\\x758d1385572900b9480372dd95d11b5b46a56651e28c7b78f0562fcc8a34731b	2026-04-17 22:05:54	f
073afcc9-f557-429a-aa29-4130b41d611b	marko.petrovic@gmail.com	\\xf316f9bc553135ea9c8f93b6990ffc9ec77075c3fed6c49f28bef678c9302755	2026-04-17 22:15:28	f
6f85c05d-5414-4d4c-8819-a67b95009439	marko.petrovic@gmail.com	\\xe92a9f0efa4b9dd44b707d1c33805409fb16353fe78ea58f8a498839d88c7c1a	2026-04-17 22:16:04	f
bdf1076c-d315-4d3f-b55d-6e2778fc0057	marko.petrovic@gmail.com	\\x795006c0e501216cbb4eb1cf837a0a2cb51173242272de6f4c11a9251cbc85e6	2026-04-17 22:18:17	f
3cbc1b6c-d67e-4c10-98c8-1e3f3da10830	marko.petrovic@gmail.com	\\x2cf8640e5562ccaf5e668fa85e5c6cfa5f2292d667e66493ceff026a839304a1	2026-04-17 22:20:21	f
55482a6b-e8f3-4625-bc90-2041ac6363e5	marko.petrovic@gmail.com	\\x3127e0e2011243f1cf33dd7eb60ffcaea4253c9456b56f188f83c1d80e5307d7	2026-04-17 22:20:29	f
799745b0-dd92-4a86-9372-7ebb76adeb60	marko.petrovic@gmail.com	\\x241d51009386fc3e2cede12f2d4272aeecab3ddc661611152ab15dca41c7c4b3	2026-04-17 22:24:08	f
ac5f4473-6086-46f8-971f-371ad89987ea	marko.petrovic@gmail.com	\\x74b77f254fe9e6bae5fb61ff292067f56b464a22fcce8f15cad0b779d1ae00c2	2026-04-17 22:28:56	f
8987fd0e-8de5-4c19-b57a-6c61ce90c2e7	marko.petrovic@gmail.com	\\x914e0fc670c94cf3e9d261d155396a0cf47bed5097825fa0144027c2689206bf	2026-04-17 22:29:13	f
111609cd-a427-4347-8127-b0372797c261	marko.petrovic@gmail.com	\\x14c5d421766ac8ca34a536bfc0bca1515cd732bf120aa8edd230fa6f87e94372	2026-04-17 22:29:26	f
c823362b-6fb6-4efc-9464-7f7f443b06df	marko.petrovic@gmail.com	\\x242c933641f2d458e2473e4296f9ad594aeb816611672e7696b4f503f105b631	2026-04-17 22:31:11	f
72698b3d-cf61-4e35-9d20-d26c589bbc88	marko.petrovic@gmail.com	\\xde93682896db03f6ea1fd48ad259e00c916987a72ba343750092162d4a2c4fec	2026-04-17 22:33:43	f
ba244767-0499-459b-bd98-c2e9d7449c3e	marko.petrovic@gmail.com	\\xa721d09e80a5084bc323349ef9ea839f95c6d5fdf1ce1a1a7445d21a56647a88	2026-04-17 22:37:41	f
b8687054-adb5-4a9f-a687-6476544b9780	marko.petrovic@gmail.com	\\x69d4ba164c9814f2a571b2cda16373e7bd277d7d2d70ac6534fe296b2ddc451a	2026-04-17 22:44:12	f
8f5a1912-ec61-4d09-9c5c-30c61740737b	marko.petrovic@gmail.com	\\x84a3caf94f087b2624e20b553208b16cd0ed683c8222a7988488c39e90f1c9ac	2026-04-17 22:51:42	f
33ba5f55-2341-44e6-bb77-286d3a717b78	marko.petrovic@gmail.com	\\x27fa06abf84c1b1590042dca8d8ce01761b060817f1a1d8e43760ab844754325	2026-04-17 22:56:37	f
480e992d-e6ff-45e8-ba59-b4aaa6bc8671	marko.petrovic@gmail.com	\\x8240bf33626469a90c2f8c2033219feba3de4f41831921cc9f68aa87de31489e	2026-04-17 23:01:56	f
\.


--
-- Data for Name: transaction_verification_codes; Type: TABLE DATA; Schema: public; Owner: -
--

COPY public.transaction_verification_codes (client_id, code, valid_until, failed_attempts, max_attempts, used, canceled, created_at) FROM stdin;
6	083984	2026-04-11 16:54:24.415862	0	3	f	f	2026-04-11 16:49:24.416336
\.


--
-- Data for Name: transfers; Type: TABLE DATA; Schema: public; Owner: -
--

COPY public.transfers (transaction_id, from_account, to_account, start_amount, end_amount, start_currency_id, exchange_rate, commission, status, "timestamp") FROM stdin;
1	333000112345678910	333000112345678920	117150.00	1000.00	1	117.15	500.00	pending	2026-04-09 10:45:45.744353
2	333000199999999130	333000199999999110	100.00	10850.00	4	1.00	1.00	completed	2026-04-09 11:58:02.234817
3	333000199999999120	333000199999999220	777.00	777.00	2	1.00	0.00	completed	2026-04-09 11:59:18.639453
4	333000199999999210	333000199999999110	77.00	77.00	1	1.00	0.00	completed	2026-04-09 12:05:28.957971
5	333000199999999110	333000199999999120	10927.00	93.00	1	0.01	109.00	completed	2026-04-09 12:29:42.698876
6	333000199999999110	333000199999999130	10000.00	92.00	1	0.01	100.00	completed	2026-04-09 12:32:48.223609
7	333000199999999110	333000199999999130	10000.00	92.00	1	0.01	100.00	completed	2026-04-09 12:37:57.375206
8	333000199999999130	333000199999999230	84.00	84.00	4	1.00	0.00	completed	2026-04-09 12:39:56.348202
9	333000199999999130	333000199999999230	100.00	100.00	4	1.00	0.00	completed	2026-04-09 12:40:19.794436
10	333000199999999130	333000199999999230	100.66	100.66	4	1.00	0.00	completed	2026-04-09 13:20:22.910506
11	333000199999999110	333000199999999130	3000.00	27.65	1	0.01	30.00	completed	2026-04-09 13:21:38.162407
12	333000199999999120	333000199999999130	100.33	108.33	2	0.01	1.00	completed	2026-04-09 13:27:40.393884
13	333000199999999120	333000199999999220	215.00	215.00	2	1.00	0.00	completed	2026-04-09 13:28:47.554928
14	333000199999999120	333000199999999220	9000.00	9000.00	2	1.00	0.00	completed	2026-04-09 13:49:06.848439
15	333000199999999220	333000199999999120	992.33	992.33	2	1.00	0.00	completed	2026-04-09 13:54:45.841007
16	333000199999999230	333000199999999130	5000.00	5000.00	4	1.00	0.00	completed	2026-04-09 20:01:12.263251
17	333000199999999120	333000199999999130	1000.00	1079.72	2	0.01	10.00	completed	2026-04-09 20:04:08.410138
18	333000199999999230	333000199999999120	284.66	263.64	4	0.01	2.85	completed	2026-04-09 20:26:11.695776
19	333000199999999110	333000199999999210	1000000.00	1000000.00	1	1.00	0.00	completed	2026-04-09 20:29:01.708718
20	333000199999999120	333000199999999110	777.77	91115.76	2	1.00	7.78	completed	2026-04-10 17:53:44.354586
21	333000199999999110	333000199999999120	77777.77	663.92	1	0.01	777.78	completed	2026-04-10 17:54:57.293668
22	333000199999999220	333000199999999130	1000.00	1079.72	2	0.01	10.00	completed	2026-04-10 18:11:08.115334
23	333000199999999110	333000199999999210	337.90	337.90	1	1.00	0.00	completed	2026-04-10 21:55:01.311024
\.


--
-- Data for Name: verification_codes; Type: TABLE DATA; Schema: public; Owner: -
--

COPY public.verification_codes (client_id, enabled, secret, temp_secret, temp_created_at) FROM stdin;
\.


--
-- Name: accounts_id_seq; Type: SEQUENCE SET; Schema: public; Owner: -
--

SELECT pg_catalog.setval('public.accounts_id_seq', 21, true);


--
-- Name: activity_codes_id_seq; Type: SEQUENCE SET; Schema: public; Owner: -
--

SELECT pg_catalog.setval('public.activity_codes_id_seq', 3, true);


--
-- Name: authorized_party_id_seq; Type: SEQUENCE SET; Schema: public; Owner: -
--

SELECT pg_catalog.setval('public.authorized_party_id_seq', 1, true);


--
-- Name: card_requests_id_seq; Type: SEQUENCE SET; Schema: public; Owner: -
--

SELECT pg_catalog.setval('public.card_requests_id_seq', 16, true);


--
-- Name: cards_id_seq; Type: SEQUENCE SET; Schema: public; Owner: -
--

SELECT pg_catalog.setval('public.cards_id_seq', 4, true);


--
-- Name: clients_id_seq; Type: SEQUENCE SET; Schema: public; Owner: -
--

SELECT pg_catalog.setval('public.clients_id_seq', 6, true);


--
-- Name: companies_id_seq; Type: SEQUENCE SET; Schema: public; Owner: -
--

SELECT pg_catalog.setval('public.companies_id_seq', 2, true);


--
-- Name: currencies_id_seq; Type: SEQUENCE SET; Schema: public; Owner: -
--

SELECT pg_catalog.setval('public.currencies_id_seq', 8, true);


--
-- Name: employees_id_seq; Type: SEQUENCE SET; Schema: public; Owner: -
--

SELECT pg_catalog.setval('public.employees_id_seq', 3, true);


--
-- Name: loan_installment_currency_id_seq; Type: SEQUENCE SET; Schema: public; Owner: -
--

SELECT pg_catalog.setval('public.loan_installment_currency_id_seq', 1, false);


--
-- Name: loan_installment_id_seq; Type: SEQUENCE SET; Schema: public; Owner: -
--

SELECT pg_catalog.setval('public.loan_installment_id_seq', 6, true);


--
-- Name: loan_request_id_seq; Type: SEQUENCE SET; Schema: public; Owner: -
--

SELECT pg_catalog.setval('public.loan_request_id_seq', 3, true);


--
-- Name: loans_currency_id_seq; Type: SEQUENCE SET; Schema: public; Owner: -
--

SELECT pg_catalog.setval('public.loans_currency_id_seq', 1, false);


--
-- Name: loans_id_seq; Type: SEQUENCE SET; Schema: public; Owner: -
--

SELECT pg_catalog.setval('public.loans_id_seq', 2, true);


--
-- Name: payment_recipients_id_seq; Type: SEQUENCE SET; Schema: public; Owner: -
--

SELECT pg_catalog.setval('public.payment_recipients_id_seq', 1, false);


--
-- Name: payments_transaction_id_seq; Type: SEQUENCE SET; Schema: public; Owner: -
--

SELECT pg_catalog.setval('public.payments_transaction_id_seq', 11, true);


--
-- Name: permissions_id_seq; Type: SEQUENCE SET; Schema: public; Owner: -
--

SELECT pg_catalog.setval('public.permissions_id_seq', 10, true);


--
-- Name: transfers_transaction_id_seq; Type: SEQUENCE SET; Schema: public; Owner: -
--

SELECT pg_catalog.setval('public.transfers_transaction_id_seq', 23, true);


--
-- Name: accounts accounts_number_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.accounts
    ADD CONSTRAINT accounts_number_key UNIQUE (number);


--
-- Name: accounts accounts_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.accounts
    ADD CONSTRAINT accounts_pkey PRIMARY KEY (id);


--
-- Name: activity_codes activity_codes_code_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.activity_codes
    ADD CONSTRAINT activity_codes_code_key UNIQUE (code);


--
-- Name: activity_codes activity_codes_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.activity_codes
    ADD CONSTRAINT activity_codes_pkey PRIMARY KEY (id);


--
-- Name: authorized_party authorized_party_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.authorized_party
    ADD CONSTRAINT authorized_party_pkey PRIMARY KEY (id);


--
-- Name: card_requests card_requests_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.card_requests
    ADD CONSTRAINT card_requests_pkey PRIMARY KEY (id);


--
-- Name: cards cards_number_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.cards
    ADD CONSTRAINT cards_number_key UNIQUE (number);


--
-- Name: cards cards_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.cards
    ADD CONSTRAINT cards_pkey PRIMARY KEY (id);


--
-- Name: clients clients_email_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.clients
    ADD CONSTRAINT clients_email_key UNIQUE (email);


--
-- Name: clients clients_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.clients
    ADD CONSTRAINT clients_pkey PRIMARY KEY (id);


--
-- Name: companies companies_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.companies
    ADD CONSTRAINT companies_pkey PRIMARY KEY (id);


--
-- Name: companies companies_registered_id_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.companies
    ADD CONSTRAINT companies_registered_id_key UNIQUE (registered_id);


--
-- Name: companies companies_tax_code_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.companies
    ADD CONSTRAINT companies_tax_code_key UNIQUE (tax_code);


--
-- Name: currencies currencies_label_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.currencies
    ADD CONSTRAINT currencies_label_key UNIQUE (label);


--
-- Name: currencies currencies_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.currencies
    ADD CONSTRAINT currencies_pkey PRIMARY KEY (id);


--
-- Name: employee_permissions employee_permissions_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.employee_permissions
    ADD CONSTRAINT employee_permissions_pkey PRIMARY KEY (employee_id, permission_id);


--
-- Name: employees employees_email_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.employees
    ADD CONSTRAINT employees_email_key UNIQUE (email);


--
-- Name: employees employees_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.employees
    ADD CONSTRAINT employees_pkey PRIMARY KEY (id);


--
-- Name: employees employees_username_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.employees
    ADD CONSTRAINT employees_username_key UNIQUE (username);


--
-- Name: exchange_rates exchange_rates_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.exchange_rates
    ADD CONSTRAINT exchange_rates_pkey PRIMARY KEY (currency_code);


--
-- Name: loan_installment loan_installment_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.loan_installment
    ADD CONSTRAINT loan_installment_pkey PRIMARY KEY (id);


--
-- Name: loan_request loan_request_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.loan_request
    ADD CONSTRAINT loan_request_pkey PRIMARY KEY (id);


--
-- Name: loans loans_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.loans
    ADD CONSTRAINT loans_pkey PRIMARY KEY (id);


--
-- Name: password_action_tokens password_action_tokens_hashed_token_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.password_action_tokens
    ADD CONSTRAINT password_action_tokens_hashed_token_key UNIQUE (hashed_token);


--
-- Name: password_action_tokens password_action_tokens_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.password_action_tokens
    ADD CONSTRAINT password_action_tokens_pkey PRIMARY KEY (email, action_type);


--
-- Name: payment_codes payment_codes_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.payment_codes
    ADD CONSTRAINT payment_codes_pkey PRIMARY KEY (code);


--
-- Name: payment_recipients payment_recipients_client_id_account_number_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.payment_recipients
    ADD CONSTRAINT payment_recipients_client_id_account_number_key UNIQUE (client_id, account_number);


--
-- Name: payment_recipients payment_recipients_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.payment_recipients
    ADD CONSTRAINT payment_recipients_pkey PRIMARY KEY (id);


--
-- Name: payments payments_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.payments
    ADD CONSTRAINT payments_pkey PRIMARY KEY (transaction_id);


--
-- Name: permissions permissions_name_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.permissions
    ADD CONSTRAINT permissions_name_key UNIQUE (name);


--
-- Name: permissions permissions_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.permissions
    ADD CONSTRAINT permissions_pkey PRIMARY KEY (id);


--
-- Name: refresh_tokens refresh_tokens_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.refresh_tokens
    ADD CONSTRAINT refresh_tokens_pkey PRIMARY KEY (session_id);


--
-- Name: transaction_verification_codes transaction_verification_codes_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.transaction_verification_codes
    ADD CONSTRAINT transaction_verification_codes_pkey PRIMARY KEY (client_id);


--
-- Name: transfers transfers_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.transfers
    ADD CONSTRAINT transfers_pkey PRIMARY KEY (transaction_id);


--
-- Name: verification_codes verification_codes_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.verification_codes
    ADD CONSTRAINT verification_codes_pkey PRIMARY KEY (client_id);


--
-- Name: idx_refresh_tokens_email; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_refresh_tokens_email ON public.refresh_tokens USING btree (email);


--
-- Name: employees trg_employee_status_change; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER trg_employee_status_change AFTER UPDATE ON public.employees FOR EACH ROW EXECUTE FUNCTION public.notify_employee_status_change();


--
-- Name: employee_permissions trg_permission_change; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER trg_permission_change AFTER INSERT OR DELETE OR UPDATE ON public.employee_permissions FOR EACH ROW EXECUTE FUNCTION public.notify_permission_change();


--
-- Name: accounts accounts_created_by_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.accounts
    ADD CONSTRAINT accounts_created_by_fkey FOREIGN KEY (created_by) REFERENCES public.employees(id) ON DELETE SET NULL;


--
-- Name: accounts accounts_currency_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.accounts
    ADD CONSTRAINT accounts_currency_fkey FOREIGN KEY (currency) REFERENCES public.currencies(label) ON UPDATE CASCADE ON DELETE RESTRICT;


--
-- Name: accounts accounts_owner_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.accounts
    ADD CONSTRAINT accounts_owner_fkey FOREIGN KEY (owner) REFERENCES public.clients(id) ON DELETE CASCADE;


--
-- Name: backup_codes backup_codes_client_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.backup_codes
    ADD CONSTRAINT backup_codes_client_id_fkey FOREIGN KEY (client_id) REFERENCES public.clients(id) ON DELETE CASCADE;


--
-- Name: card_requests card_requests_account_number_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.card_requests
    ADD CONSTRAINT card_requests_account_number_fkey FOREIGN KEY (account_number) REFERENCES public.accounts(number) ON UPDATE CASCADE ON DELETE RESTRICT;


--
-- Name: cards cards_account_number_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.cards
    ADD CONSTRAINT cards_account_number_fkey FOREIGN KEY (account_number) REFERENCES public.accounts(number) ON UPDATE CASCADE ON DELETE RESTRICT;


--
-- Name: companies companies_activity_code_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.companies
    ADD CONSTRAINT companies_activity_code_id_fkey FOREIGN KEY (activity_code_id) REFERENCES public.activity_codes(id) ON UPDATE CASCADE ON DELETE RESTRICT;


--
-- Name: companies companies_owner_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.companies
    ADD CONSTRAINT companies_owner_id_fkey FOREIGN KEY (owner_id) REFERENCES public.clients(id) ON UPDATE CASCADE ON DELETE RESTRICT;


--
-- Name: employee_permissions employee_permissions_employee_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.employee_permissions
    ADD CONSTRAINT employee_permissions_employee_id_fkey FOREIGN KEY (employee_id) REFERENCES public.employees(id) ON DELETE CASCADE;


--
-- Name: employee_permissions employee_permissions_permission_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.employee_permissions
    ADD CONSTRAINT employee_permissions_permission_id_fkey FOREIGN KEY (permission_id) REFERENCES public.permissions(id) ON DELETE CASCADE;


--
-- Name: loan_installment loan_installment_currency_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.loan_installment
    ADD CONSTRAINT loan_installment_currency_id_fkey FOREIGN KEY (currency_id) REFERENCES public.currencies(id) ON UPDATE CASCADE ON DELETE RESTRICT;


--
-- Name: loan_installment loan_installment_loan_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.loan_installment
    ADD CONSTRAINT loan_installment_loan_id_fkey FOREIGN KEY (loan_id) REFERENCES public.loans(id) ON UPDATE CASCADE ON DELETE CASCADE;


--
-- Name: loan_request loan_request_account_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.loan_request
    ADD CONSTRAINT loan_request_account_id_fkey FOREIGN KEY (account_id) REFERENCES public.accounts(id) ON UPDATE CASCADE ON DELETE RESTRICT;


--
-- Name: loan_request loan_request_currency_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.loan_request
    ADD CONSTRAINT loan_request_currency_id_fkey FOREIGN KEY (currency_id) REFERENCES public.currencies(id) ON UPDATE CASCADE ON DELETE RESTRICT;


--
-- Name: loans loans_account_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.loans
    ADD CONSTRAINT loans_account_id_fkey FOREIGN KEY (account_id) REFERENCES public.accounts(id) ON UPDATE CASCADE ON DELETE RESTRICT;


--
-- Name: loans loans_currency_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.loans
    ADD CONSTRAINT loans_currency_id_fkey FOREIGN KEY (currency_id) REFERENCES public.currencies(id) ON UPDATE CASCADE ON DELETE RESTRICT;


--
-- Name: payment_recipients payment_recipients_client_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.payment_recipients
    ADD CONSTRAINT payment_recipients_client_id_fkey FOREIGN KEY (client_id) REFERENCES public.clients(id) ON DELETE CASCADE;


--
-- Name: payments payments_from_account_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.payments
    ADD CONSTRAINT payments_from_account_fkey FOREIGN KEY (from_account) REFERENCES public.accounts(number);


--
-- Name: payments payments_recipient_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.payments
    ADD CONSTRAINT payments_recipient_id_fkey FOREIGN KEY (recipient_id) REFERENCES public.clients(id);


--
-- Name: payments payments_to_account_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.payments
    ADD CONSTRAINT payments_to_account_fkey FOREIGN KEY (to_account) REFERENCES public.accounts(number);


--
-- Name: transaction_verification_codes transaction_verification_codes_client_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.transaction_verification_codes
    ADD CONSTRAINT transaction_verification_codes_client_id_fkey FOREIGN KEY (client_id) REFERENCES public.clients(id) ON DELETE CASCADE;


--
-- Name: transfers transfers_from_account_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.transfers
    ADD CONSTRAINT transfers_from_account_fkey FOREIGN KEY (from_account) REFERENCES public.accounts(number);


--
-- Name: transfers transfers_start_currency_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.transfers
    ADD CONSTRAINT transfers_start_currency_id_fkey FOREIGN KEY (start_currency_id) REFERENCES public.currencies(id) ON UPDATE CASCADE ON DELETE RESTRICT;


--
-- Name: transfers transfers_to_account_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.transfers
    ADD CONSTRAINT transfers_to_account_fkey FOREIGN KEY (to_account) REFERENCES public.accounts(number);


--
-- Name: verification_codes verification_codes_client_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.verification_codes
    ADD CONSTRAINT verification_codes_client_id_fkey FOREIGN KEY (client_id) REFERENCES public.clients(id) ON DELETE CASCADE;


--
-- PostgreSQL database dump complete
--

\unrestrict nIbCOnMOZvfbTJURECJQ3Nd0vODoWTTFYxbD6hBW8gg2hDohDedMzaOGpRj7beX

