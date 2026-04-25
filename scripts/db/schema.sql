CREATE TABLE IF NOT EXISTS permissions (
    id   BIGSERIAL PRIMARY KEY,
    name VARCHAR(100) UNIQUE NOT NULL
);

CREATE TABLE IF NOT EXISTS employees (
    id            BIGSERIAL    PRIMARY KEY,
    first_name    VARCHAR(100) NOT NULL,
    last_name     VARCHAR(100) NOT NULL,
    date_of_birth DATE         NOT NULL,
    gender        VARCHAR(1)   NOT NULL,
    email         VARCHAR(255) UNIQUE NOT NULL,
    phone_number  VARCHAR(20)  NOT NULL,
    address       VARCHAR(255) NOT NULL,
    username      VARCHAR(100) UNIQUE NOT NULL,
    password      BYTEA        NOT NULL,
    salt_password BYTEA        NOT NULL,
    position      VARCHAR(100) NOT NULL,
    department    VARCHAR(100) NOT NULL,
    active        BOOLEAN      NOT NULL DEFAULT true,
    -- Daily trading limit (in RSD minor units) for employees with the `agent` permission.
    -- Supervisors and admins ignore the limit.
    "limit"       BIGINT       NOT NULL DEFAULT 0 CHECK ("limit" >= 0),
    used_limit    BIGINT       NOT NULL DEFAULT 0 CHECK (used_limit >= 0),
    need_approval BOOLEAN      NOT NULL DEFAULT false,
    created_at    TIMESTAMP    NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMP    NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS employee_permissions (
    employee_id   BIGINT NOT NULL REFERENCES employees(id) ON DELETE CASCADE,
    permission_id BIGINT NOT NULL REFERENCES permissions(id) ON DELETE CASCADE,
    PRIMARY KEY (employee_id, permission_id)
);

CREATE TABLE IF NOT EXISTS clients (
    id            BIGSERIAL    PRIMARY KEY,
    first_name    VARCHAR(100) NOT NULL,
    last_name     VARCHAR(100) NOT NULL,
    date_of_birth DATE         NOT NULL,
    gender        VARCHAR(1)   NOT NULL,
    email         VARCHAR(255) UNIQUE NOT NULL,
    phone_number  VARCHAR(20)  NOT NULL,
    address       VARCHAR(255) NOT NULL,
    password      BYTEA        NOT NULL,
    salt_password BYTEA        NOT NULL,
    created_at    TIMESTAMP    NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMP    NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS payment_recipients (
    id              BIGSERIAL    PRIMARY KEY,
    client_id       BIGINT       NOT NULL REFERENCES clients(id) ON DELETE CASCADE,
    name            VARCHAR(127) NOT NULL,
    account_number  VARCHAR(20)  NOT NULL,
    created_at      TIMESTAMP    NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMP    NOT NULL DEFAULT NOW(),
    UNIQUE (client_id, account_number)
);

CREATE TABLE IF NOT EXISTS refresh_tokens (
    email        VARCHAR(255) PRIMARY KEY,
    hashed_token BYTEA        NOT NULL,
    valid_until  TIMESTAMP    NOT NULL,
    revoked      BOOLEAN      NOT NULL DEFAULT FALSE
);

CREATE TABLE IF NOT EXISTS password_action_tokens (
    email        VARCHAR(255) NOT NULL,
    action_type  VARCHAR(20)  NOT NULL,
    hashed_token BYTEA        NOT NULL UNIQUE,
    valid_until  TIMESTAMP    NOT NULL,
    used         BOOLEAN      NOT NULL DEFAULT FALSE,
    created_at   TIMESTAMP    NOT NULL DEFAULT NOW(),
    used_at      TIMESTAMP,
    PRIMARY KEY (email, action_type),
    CHECK (action_type IN ('reset', 'initial_set', 'totp_disable'))
);

CREATE TABLE IF NOT EXISTS currencies (
    id              BIGSERIAL       PRIMARY KEY,
    label           VARCHAR(8)      NOT NULL,
    name            VARCHAR(64)     NOT NULL,
    symbol          VARCHAR(8)      NOT NULL,
    countries       TEXT            NOT NULL,
    description     VARCHAR(1023)   NOT NULL,
    active          BOOLEAN NOT     NULL DEFAULT TRUE,
    UNIQUE(label)
);

CREATE TABLE IF NOT EXISTS activity_codes (
    id BIGSERIAL PRIMARY KEY,
    code VARCHAR(7) NOT NULL,
    sector VARCHAR(127) NOT NULL,
    branch VARCHAR(255) NOT NULL,
    UNIQUE(code)
);

CREATE TABLE IF NOT EXISTS companies (
    id                  BIGSERIAL        PRIMARY KEY,
    registered_id       BIGINT          NOT NULL,
    name                VARCHAR(127)    NOT NULL,
    tax_code            BIGINT          NOT NULL,
    activity_code_id    BIGINT          REFERENCES activity_codes(id) ON UPDATE CASCADE ON DELETE RESTRICT,
    address             VARCHAR(255)    NOT NULL,
    owner_id            BIGINT          NOT NULL REFERENCES clients(id) ON UPDATE CASCADE ON DELETE RESTRICT,
    UNIQUE(registered_id),
    UNIQUE(tax_code)
);

CREATE TYPE owner_type AS ENUM ('personal', 'business');
CREATE TYPE account_type AS ENUM ('checking', 'foreign');

CREATE TABLE IF NOT EXISTS accounts (
    id                  BIGSERIAL       PRIMARY KEY,
    number              VARCHAR(20)     NOT NULL,
    name                VARCHAR(127)    NOT NULL,
    owner               BIGINT          NOT NULL REFERENCES clients(id) ON DELETE CASCADE,
    company_id          BIGINT          DEFAULT NULL REFERENCES companies(id) ON DELETE CASCADE,
    balance             BIGINT          NOT NULL DEFAULT 0,
    created_by          BIGINT          REFERENCES employees(id) ON DELETE SET NULL,
    created_at          DATE            NOT NULL DEFAULT CURRENT_DATE,
    valid_until         DATE            NOT NULL,
    currency            VARCHAR(8)      NOT NULL REFERENCES currencies(label) ON UPDATE CASCADE ON DELETE RESTRICT,
    active              BOOLEAN         NOT NULL DEFAULT FALSE,
    owner_type          owner_type      NOT NULL,
    account_type        account_type    NOT NULL,
    maintainance_cost   BIGINT          NOT NULL,
    daily_limit         BIGINT,
    monthly_limit       BIGINT,
    daily_expenditure   BIGINT,
    monthly_expenditure BIGINT,
    UNIQUE(number)
);

CREATE TYPE card_type AS ENUM ('debit', 'credit');
CREATE TYPE card_status AS ENUM ('active', 'blocked');
CREATE TYPE card_brand AS ENUM ('visa', 'mastercard', 'amex', 'dinacard');

CREATE TABLE IF NOT EXISTS cards (
    id              BIGSERIAL        PRIMARY KEY,
    number          VARCHAR(20)     NOT NULL,
    type            card_type       NOT NULL DEFAULT 'debit',
    brand           card_brand       NOT NULL,
    creation_date   DATE            NOT NULL DEFAULT CURRENT_DATE,
    valid_until     DATE            NOT NULL,
    account_number  VARCHAR(20)     REFERENCES accounts(number) ON UPDATE CASCADE ON DELETE RESTRICT,
    cvv             VARCHAR(7)      NOT NULL,
    card_limit      BIGINT,
    status          card_status     NOT NULL DEFAULT 'active',
    UNIQUE(number)
);

CREATE TABLE IF NOT EXISTS card_requests (
    id              BIGSERIAL       PRIMARY KEY,
    account_number  VARCHAR(20)     REFERENCES accounts(number) ON UPDATE CASCADE ON DELETE RESTRICT,
    type            card_type       NOT NULL DEFAULT 'debit',
    brand           card_brand      NOT NULL,
    token           VARCHAR(255)    NOT NULL,
    expiration_date DATE            NOT NULL,
    complete        BOOLEAN         NOT NULL DEFAULT FALSE,
    email           VARCHAR(255)    NOT NULL
);

CREATE TABLE IF NOT EXISTS authorized_party (
    id              BIGSERIAL       PRIMARY KEY,
    name            VARCHAR(63)     NOT NULL,
    last_name       VARCHAR(63)     NOT NULL,
    date_of_birth   DATE            NOT NULL,
    gender          VARCHAR(7)      NOT NULL,
    email           VARCHAR(127)    NOT NULL,
    phone_number    VARCHAR(15)     NOT NULL,
    address         VARCHAR (255)   NOT NULL
);

CREATE TABLE IF NOT EXISTS payments (
    transaction_id      BIGSERIAL       PRIMARY KEY,
    from_account        VARCHAR(20)     REFERENCES accounts(number),
    to_account          VARCHAR(20)     REFERENCES accounts(number),
    start_amount        BIGINT          NOT NULL,
    end_amount          BIGINT          NOT NULL,
    commission          BIGINT          NOT NULL,
    status              VARCHAR(20)     NOT NULL DEFAULT 'realized' CHECK (status IN ('realized', 'rejected', 'pending')),
    recipient_id        BIGINT          REFERENCES clients(id),
    transcaction_code    INT            NOT NULL,
    call_number         VARCHAR(31)     NOT NULL,
    reason              VARCHAR(255)    NOT NULL,
    timestamp           TIMESTAMP       NOT NULL DEFAULT NOW()
);


CREATE TYPE transfer_status AS ENUM ('pending', 'realized', 'rejected');

CREATE TABLE IF NOT EXISTS transfers (
    transaction_id      BIGSERIAL       PRIMARY KEY,
    from_account        VARCHAR(20)     REFERENCES accounts(number),
    to_account          VARCHAR(20)     REFERENCES accounts(number),
    start_amount        BIGINT          NOT NULL,
    end_amount          BIGINT          NOT NULL,
    start_currency_id   BIGINT          REFERENCES currencies(id) ON UPDATE CASCADE ON DELETE RESTRICT,
    exchange_rate       DECIMAL(20,2),
    commission          BIGINT          NOT NULL,
    status              transfer_status  NOT NULL DEFAULT 'pending',
    timestamp           TIMESTAMP       NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS payment_codes (
    code        BIGINT          PRIMARY KEY,
    description VARCHAR(255)    NOT NULL
);

CREATE TYPE loan_type AS ENUM ('cash', 'mortgage', 'car', 'refinancing', 'student');
CREATE TYPE loan_status AS ENUM ('approved', 'rejected', 'paid', 'late');
CREATE TYPE interest_rate_type AS ENUM ('fixed', 'variable');

CREATE TABLE IF NOT EXISTS loans (
    id                  BIGSERIAL           PRIMARY KEY,
    account_id          BIGINT              REFERENCES accounts(id) ON UPDATE CASCADE ON DELETE RESTRICT,
    amount              BIGINT              NOT NULL,
    currency_id         BIGSERIAL           REFERENCES currencies(id) ON UPDATE CASCADE ON DELETE RESTRICT,
    installments        BIGINT              NOT NULL,
    nominal_rate        DECIMAL (5, 2)      NOT NULL,
    interest_rate       DECIMAL (5, 2)      NOT NULL,
    date_signed         DATE                NOT NULL,
    date_end            DATE                NOT NULL,
    monthly_payment     BIGINT              NOT NULL,
    next_payment_due    DATE                NOT NULL,
    remaining_debt      BIGINT              NOT NULL,
    type                loan_type           NOT NULL,
    loan_status         loan_status         NOT NULL DEFAULT 'approved',
    interest_rate_type  interest_rate_type  NOT NULL
);

CREATE TYPE installment_status AS ENUM ('paid', 'due', 'late');

CREATE TABLE IF NOT EXISTS loan_installment (
    id                  BIGSERIAL           PRIMARY KEY,
    loan_id             BIGINT              REFERENCES loans(id) ON UPDATE CASCADE ON DELETE CASCADE,
    installment_amount  BIGINT              NOT NULL,
    interest_rate       DECIMAL(5, 2)       NOT NULL,
    currency_id         BIGSERIAL           REFERENCES currencies(id) ON UPDATE CASCADE ON DELETE RESTRICT,
    due_date            DATE                NOT NULL,
    paid_date           DATE                NOT NULL,
    status              installment_status  NOT NULL DEFAULT 'due'
);

CREATE TYPE employment_status AS ENUM ('full_time', 'temporary', 'unemployed'); -- unused due to this change, remove later?
CREATE TYPE loan_request_status AS ENUM ('pending', 'approved', 'rejected');

-- I will revert the previous DB change in sprint 3 in case it was meant to be used for employee loan endpoints later
CREATE TABLE IF NOT EXISTS loan_request (
    id                  BIGSERIAL            PRIMARY KEY,
    type                loan_type            NOT NULL,
    currency_id         BIGINT               REFERENCES currencies(id) ON UPDATE CASCADE ON DELETE RESTRICT,
    amount              BIGINT               NOT NULL,
    repayment_period    BIGINT               NOT NULL,
    account_id          BIGINT               REFERENCES accounts(id) ON UPDATE CASCADE ON DELETE RESTRICT,
    status              loan_request_status  NOT NULL DEFAULT 'pending',
    submission_date     TIMESTAMP            NOT NULL DEFAULT NOW(),
    purpose             VARCHAR(255),
    salary              BIGINT,
    employment_status   employment_status,
    employment_period   BIGINT,
    phone_number        VARCHAR(32),
    interest_rate_type  interest_rate_type   NOT NULL DEFAULT 'fixed'
);

CREATE TABLE IF NOT EXISTS verification_codes (
    client_id       BIGINT      PRIMARY KEY REFERENCES clients(id) ON DELETE CASCADE,
    enabled         BOOLEAN     NOT NULL DEFAULT FALSE,
    secret          VARCHAR(32),
    temp_secret     VARCHAR(32),
    temp_created_at TIMESTAMP   NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS backup_codes (
    client_id BIGINT REFERENCES clients(id) ON DELETE CASCADE,
    token     VARCHAR(6) NOT NULL,
    used      BOOLEAN NOT NULL DEFAULT FALSE
);

CREATE TABLE IF NOT EXISTS exchange_rates (
    currency_code VARCHAR(3)     PRIMARY KEY,
    rate_to_rsd   DECIMAL(20, 6) NOT NULL,
    updated_at    TIMESTAMP      NOT NULL DEFAULT NOW(),
    valid_until   TIMESTAMP      NOT NULL DEFAULT NOW()
);

-- ============================================================================
-- Trading: securities, listings, orders
-- ============================================================================

-- open_time / close_time are the exchange's local-time working hours
-- (see spec p.40). All exchanges within the same polity share the same
-- hours and holiday calendar; the holiday table is hardcoded in Go
-- (internal/trading/hours.go) rather than modeled here.
CREATE TABLE IF NOT EXISTS exchanges (
    id                  BIGSERIAL       PRIMARY KEY,
    name                VARCHAR(127)    NOT NULL,
    acronym             VARCHAR(31)     NOT NULL,
    mic_code            VARCHAR(8)      NOT NULL,
    polity              VARCHAR(127)    NOT NULL,
    currency            VARCHAR(8)      NOT NULL REFERENCES currencies(label) ON UPDATE CASCADE ON DELETE RESTRICT,
    time_zone_offset    VARCHAR(8)      NOT NULL,
    open_time           TIME            NOT NULL DEFAULT '09:30',
    close_time          TIME            NOT NULL DEFAULT '16:00',
    open_override       BOOLEAN         NOT NULL DEFAULT FALSE,
    UNIQUE(mic_code)
);

CREATE TABLE IF NOT EXISTS stocks (
    id                  BIGSERIAL       PRIMARY KEY,
    ticker              VARCHAR(8)      NOT NULL,
    name                VARCHAR(127)    NOT NULL,
    outstanding_shares  BIGINT          NOT NULL DEFAULT 0,
    dividend_yield      DECIMAL(10, 6)  NOT NULL DEFAULT 0,
    UNIQUE(ticker)
);

CREATE TABLE IF NOT EXISTS futures (
    id                  BIGSERIAL       PRIMARY KEY,
    ticker              VARCHAR(16)     NOT NULL,
    name                VARCHAR(127)    NOT NULL,
    contract_size       BIGINT          NOT NULL,
    contract_unit       VARCHAR(31)     NOT NULL,
    settlement_date     DATE            NOT NULL,
    UNIQUE(ticker)
);

CREATE TYPE forex_liquidity AS ENUM ('high', 'medium', 'low');

CREATE TABLE IF NOT EXISTS forex_pairs (
    id                  BIGSERIAL       PRIMARY KEY,
    ticker              VARCHAR(8)      NOT NULL,
    name                VARCHAR(127)    NOT NULL,
    base_currency       VARCHAR(8)      NOT NULL REFERENCES currencies(label) ON UPDATE CASCADE ON DELETE RESTRICT,
    quote_currency      VARCHAR(8)      NOT NULL REFERENCES currencies(label) ON UPDATE CASCADE ON DELETE RESTRICT,
    exchange_rate       DECIMAL(20, 6)  NOT NULL,
    liquidity           forex_liquidity NOT NULL DEFAULT 'medium',
    UNIQUE(ticker),
    UNIQUE(base_currency, quote_currency)
);

CREATE TYPE option_type AS ENUM ('call', 'put');

CREATE TABLE IF NOT EXISTS options (
    id                  BIGSERIAL       PRIMARY KEY,
    ticker              VARCHAR(32)     NOT NULL,
    name                VARCHAR(127)    NOT NULL,
    stock_id            BIGINT          NOT NULL REFERENCES stocks(id) ON UPDATE CASCADE ON DELETE CASCADE,
    option_type         option_type     NOT NULL,
    strike_price        BIGINT          NOT NULL,
    premium             BIGINT          NOT NULL,
    implied_volatility  DECIMAL(10, 4)  NOT NULL DEFAULT 0,
    open_interest       BIGINT          NOT NULL DEFAULT 0,
    settlement_date     DATE            NOT NULL,
    UNIQUE(ticker)
);

-- A listing pairs a tradable security (stock or future) with an exchange
-- and carries the current market-data snapshot. ForexPairs have no listings
-- (spec: "Ideja 1 — ForexPairs nemaju listinge").
-- Options have no listings either; they hang off their underlying stock.
CREATE TABLE IF NOT EXISTS listings (
    id                  BIGSERIAL       PRIMARY KEY,
    exchange_id         BIGINT          NOT NULL REFERENCES exchanges(id) ON UPDATE CASCADE ON DELETE RESTRICT,
    stock_id            BIGINT          REFERENCES stocks(id) ON UPDATE CASCADE ON DELETE CASCADE,
    future_id           BIGINT          REFERENCES futures(id) ON UPDATE CASCADE ON DELETE CASCADE,
    last_refresh        TIMESTAMP       NOT NULL DEFAULT NOW(),
    price               BIGINT          NOT NULL DEFAULT 0,
    ask_price           BIGINT          NOT NULL DEFAULT 0,
    bid_price           BIGINT          NOT NULL DEFAULT 0,
    CHECK ((stock_id IS NOT NULL)::int + (future_id IS NOT NULL)::int = 1)
);

CREATE TABLE IF NOT EXISTS listing_daily_price_info (
    id                  BIGSERIAL       PRIMARY KEY,
    listing_id          BIGINT          NOT NULL REFERENCES listings(id) ON UPDATE CASCADE ON DELETE CASCADE,
    date                DATE            NOT NULL,
    price               BIGINT          NOT NULL,
    ask_price           BIGINT          NOT NULL,
    bid_price           BIGINT          NOT NULL,
    change              BIGINT          NOT NULL DEFAULT 0,
    volume              BIGINT          NOT NULL DEFAULT 0,
    UNIQUE(listing_id, date)
);

-- Orders can be placed by either a client or an employee (actuary); the
-- order_placers table holds exactly one of the two, so `orders` carries a
-- single FK regardless of the placer's kind.
CREATE TABLE IF NOT EXISTS order_placers (
    id                  BIGSERIAL       PRIMARY KEY,
    client_id           BIGINT          REFERENCES clients(id) ON UPDATE CASCADE ON DELETE CASCADE,
    employee_id         BIGINT          REFERENCES employees(id) ON UPDATE CASCADE ON DELETE CASCADE,
    CHECK ((client_id IS NOT NULL)::int + (employee_id IS NOT NULL)::int = 1)
);

-- One placer row per client / employee identity. Holdings (#207) are keyed on
-- placer_id, so the row has to outlive any single order — the partial indexes
-- enforce this without breaking the polymorphic NULL pattern.
CREATE UNIQUE INDEX IF NOT EXISTS order_placers_client_uniq   ON order_placers(client_id)   WHERE client_id   IS NOT NULL;
CREATE UNIQUE INDEX IF NOT EXISTS order_placers_employee_uniq ON order_placers(employee_id) WHERE employee_id IS NOT NULL;

CREATE TYPE order_type AS ENUM ('market', 'limit', 'stop', 'stop_limit');
CREATE TYPE order_direction AS ENUM ('buy', 'sell');
-- 'cancelled' distinguishes supervisor/owner withdrawals from supervisor-declined
-- orders: declined is for pending orders that never went live (and get a
-- commission refund); cancelled is for orders that were approved but are being
-- withdrawn against their remaining unfilled portion (spec pp.57–58, #204).
CREATE TYPE order_status AS ENUM ('pending', 'approved', 'declined', 'done', 'cancelled');

CREATE TABLE IF NOT EXISTS orders (
    id                  BIGSERIAL       PRIMARY KEY,
    placer_id           BIGINT          NOT NULL REFERENCES order_placers(id) ON UPDATE CASCADE ON DELETE RESTRICT,
    listing_id          BIGINT          REFERENCES listings(id) ON UPDATE CASCADE ON DELETE RESTRICT,
    option_id           BIGINT          REFERENCES options(id) ON UPDATE CASCADE ON DELETE RESTRICT,
    forex_pair_id       BIGINT          REFERENCES forex_pairs(id) ON UPDATE CASCADE ON DELETE RESTRICT,
    -- Debit target for the placer side of each fill. Kept on the row so the
    -- execution engine can settle fills without re-reading the original
    -- request; also lets decline/cancel refund commission if we ever wire it.
    account_number      VARCHAR(20)     NOT NULL REFERENCES accounts(number) ON UPDATE CASCADE ON DELETE RESTRICT,
    order_type          order_type      NOT NULL,
    direction           order_direction NOT NULL,
    status              order_status    NOT NULL DEFAULT 'pending',
    quantity            BIGINT          NOT NULL,
    contract_size       BIGINT          NOT NULL DEFAULT 1,
    price_per_unit      BIGINT          NOT NULL,
    -- stop_price holds the activation trigger for stop_limit orders (where
    -- price_per_unit already carries the limit). Stop orders keep the trigger
    -- in price_per_unit for backward compatibility and leave this at 0.
    stop_price          BIGINT          NOT NULL DEFAULT 0,
    -- triggered_at is set once a stop / stop_limit order's activation
    -- condition is first met; after that the executor treats it like a
    -- plain market / limit respectively. NULL = not yet activated. Persisted
    -- so restarts don't re-arm an already-triggered order.
    triggered_at        TIMESTAMP,
    remaining_portions  BIGINT          NOT NULL,
    commission          BIGINT          NOT NULL DEFAULT 0,
    approved_by         BIGINT          REFERENCES employees(id) ON UPDATE CASCADE ON DELETE SET NULL,
    is_done             BOOLEAN         NOT NULL DEFAULT FALSE,
    after_hours         BOOLEAN         NOT NULL DEFAULT FALSE,
    all_or_none         BOOLEAN         NOT NULL DEFAULT FALSE,
    margin              BOOLEAN         NOT NULL DEFAULT FALSE,
    last_modification   TIMESTAMP       NOT NULL DEFAULT NOW(),
    created_at          TIMESTAMP       NOT NULL DEFAULT NOW(),
    CHECK ((listing_id IS NOT NULL)::int + (option_id IS NOT NULL)::int + (forex_pair_id IS NOT NULL)::int = 1)
);

-- Portfolio holdings (#207, spec p.62). Polymorphic over the four asset
-- kinds — stock / future / forex_pair / option — same exactly-one pattern
-- as orders.asset. Keyed by (placer_id, asset) so a buy-fill upserts the
-- existing row instead of creating duplicates; account_id records where
-- subsequent sell proceeds should land (relevant for tax). public_amount
-- is the OTC-discoverable share count (stocks only); the OTC flow itself
-- ships in the fourth celina, this column is just the counter.
CREATE TABLE IF NOT EXISTS holdings (
    id              BIGSERIAL       PRIMARY KEY,
    placer_id       BIGINT          NOT NULL REFERENCES order_placers(id) ON UPDATE CASCADE ON DELETE CASCADE,
    stock_id        BIGINT          REFERENCES stocks(id)       ON UPDATE CASCADE ON DELETE CASCADE,
    future_id       BIGINT          REFERENCES futures(id)      ON UPDATE CASCADE ON DELETE CASCADE,
    forex_pair_id   BIGINT          REFERENCES forex_pairs(id)  ON UPDATE CASCADE ON DELETE CASCADE,
    option_id       BIGINT          REFERENCES options(id)      ON UPDATE CASCADE ON DELETE CASCADE,
    account_id      BIGINT          NOT NULL REFERENCES accounts(id) ON UPDATE CASCADE ON DELETE RESTRICT,
    amount          BIGINT          NOT NULL DEFAULT 0 CHECK (amount >= 0),
    avg_cost        BIGINT          NOT NULL DEFAULT 0 CHECK (avg_cost >= 0),
    public_amount   BIGINT          NOT NULL DEFAULT 0 CHECK (public_amount >= 0),
    last_modified   TIMESTAMP       NOT NULL DEFAULT NOW(),
    CHECK ((stock_id IS NOT NULL)::int + (future_id IS NOT NULL)::int + (forex_pair_id IS NOT NULL)::int + (option_id IS NOT NULL)::int = 1),
    -- public_amount is meaningful only for stocks; everywhere else it stays 0.
    CHECK (stock_id IS NOT NULL OR public_amount = 0)
);

-- One holding per (placer, asset). Partial unique indexes keep the polymorphic
-- shape working with NULLs (default Postgres NULLS DISTINCT would otherwise
-- let two stock_id-NULL rows coexist on the same future_id).
CREATE UNIQUE INDEX IF NOT EXISTS holdings_placer_stock_uniq  ON holdings(placer_id, stock_id)      WHERE stock_id      IS NOT NULL;
CREATE UNIQUE INDEX IF NOT EXISTS holdings_placer_future_uniq ON holdings(placer_id, future_id)     WHERE future_id     IS NOT NULL;
CREATE UNIQUE INDEX IF NOT EXISTS holdings_placer_forex_uniq  ON holdings(placer_id, forex_pair_id) WHERE forex_pair_id IS NOT NULL;
CREATE UNIQUE INDEX IF NOT EXISTS holdings_placer_option_uniq ON holdings(placer_id, option_id)     WHERE option_id     IS NOT NULL;

-- Per-chunk fills written by the execution engine (#205). price_per_unit is
-- in the instrument's currency; fx_rate is NULL for same-currency fills and
-- otherwise stores the rateInstrRSD/rateAccRSD snapshot used to convert the
-- chunk cost into the placer's account currency at settle time.
CREATE TABLE IF NOT EXISTS order_fills (
    id                  BIGSERIAL        PRIMARY KEY,
    order_id            BIGINT           NOT NULL REFERENCES orders(id) ON UPDATE CASCADE ON DELETE CASCADE,
    portions            BIGINT           NOT NULL,
    price_per_unit      BIGINT           NOT NULL,
    fx_rate             DOUBLE PRECISION,
    created_at          TIMESTAMP        NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_order_fills_order ON order_fills(order_id);

-- Notify Redis when employee permissions change
CREATE OR REPLACE FUNCTION notify_permission_change() RETURNS trigger AS $$
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
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_permission_change
    AFTER INSERT OR UPDATE OR DELETE ON employee_permissions
    FOR EACH ROW EXECUTE FUNCTION notify_permission_change();

-- Notify Redis when employee active status changes
CREATE OR REPLACE FUNCTION notify_employee_status_change() RETURNS trigger AS $$
BEGIN
    IF OLD.active IS DISTINCT FROM NEW.active THEN
        PERFORM pg_notify('permission_change', NEW.email);
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_employee_status_change
    AFTER UPDATE ON employees
    FOR EACH ROW EXECUTE FUNCTION notify_employee_status_change();
