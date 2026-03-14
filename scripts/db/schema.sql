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
    CHECK (action_type IN ('reset', 'initial_set'))
);

CREATE TABLE IF NOT EXISTS currency (
    label           VARCHAR(8)      PRIMARY KEY,
    name            VARCHAR(64)     NOT NULL,
    symbol          VARCHAR(8)      NOT NULL,
    countries       TEXT            NOT NULL,
    description     VARCHAR(1023)   NOT NULL,
    active          BOOLEAN NOT     NULL DEFAULT TRUE
);

CREATE TYPE owner_type AS ENUM ('personal', 'business');
CREATE TYPE account_type AS ENUM ('checking', 'foreign');

CREATE TABLE IF NOT EXISTS account (
    number              VARCHAR(20)     PRIMARY KEY,
    name                VARCHAR(127)    NOT NULL,
    owner               BIGINT          NOT NULL REFERENCES clients(id) ON DELETE CASCADE, -- cascade delete??
    balance             BIGINT          NOT NULL DEFAULT 0,
    created_by          BIGINT          NOT NULL REFERENCES employees(id) ON DELETE SET NULL,
    created_at          DATE            NOT NULL DEFAULT CURRENT_DATE,
    valid_until         DATE            NOT NULL,
    currency            VARCHAR(8)      NOT NULL REFERENCES currency(label) ON UPDATE CASCADE ON DELETE RESTRICT,
    active              BOOLEAN         NOT NULL DEFAULT FALSE,
    owner_type          owner_type      NOT NULL,
    account_type        account_type   NOT NULL,
    maintainance_cost   BIGINT          NOT NULL,
    daily_limit         BIGINT,
    monthly_limit       BIGINT,
    daily_expenditure   BIGINT,
    monthly_expenditure BIGINT
);

CREATE TABLE IF NOT EXISTS activity_code (
    id BIGSERIAL PRIMARY KEY,
    code VARCHAR(7) NOT NULL,
    sector VARCHAR(127) NOT NULL,
    branch VARCHAR(255) NOT NULL
);

CREATE TABLE IF NOT EXISTS company (
    registered_id       BIGINT          PRIMARY KEY,
    name                VARCHAR(127)    NOT NULL,
    tax_code            BIGINT          NOT NULL,
    activity_code_id    BIGINT          REFERENCES activity_code(id) ON UPDATE CASCADE ON DELETE RESTRICT,
    address             VARCHAR(255)    NOT NULL,
    owner_id            BIGINT          NOT NULL REFERENCES clients(id) ON UPDATE CASCADE ON DELETE RESTRICT
);

CREATE TYPE card_type AS ENUM ('debit', 'credit');

CREATE TABLE IF NOT EXISTS card (
    number          VARCHAR(20)     PRIMARY KEY,
    type            card_type       NOT NULL DEFAULT 'debit',
    name            VARCHAR(127)    NOT NULL,
    creation_date   DATE            NOT NULL DEFAULT CURRENT_DATE,
    valid_until     DATE            NOT NULL,
    account_number  VARCHAR(20)     REFERENCES account(number) ON UPDATE CASCADE ON DELETE RESTRICT,
    cvv             VARCHAR(7)      NOT NULL,
    card_limit      BIGINT,
    active          BOOLEAN         NOT NULL DEFAULT FALSE
);

CREATE TABLE IF NOT EXISTS ovlasceno_lice (
    id              BIGSERIAL       PRIMARY KEY,
    ime             VARCHAR(63)     NOT NULL,
    prezime         VARCHAR(63)     NOT NULL,
    datum_rodjenja  DATE            NOT NULL,
    pol             VARCHAR(7)      NOT NULL,
    email           VARCHAR(127)    NOT NULL,
    phone_number    VARCHAR(15)     NOT NULL,
    address         VARCHAR (255)   NOT NULL
);

CREATE TABLE IF NOT EXISTS payment (
    transaction_id      BIGSERIAL       PRIMARY KEY,
    from_account        VARCHAR(20)     REFERENCES account(number),
    to_account          VARCHAR(20)     REFERENCES account(number),
    start_amount        BIGINT          NOT NULL,
    end_amount          BIGINT          NOT NULL,
    commission          BIGINT          NOT NULL,
    recipient_id        BIGINT          REFERENCES clients(id),
    trascaction_code    INT             NOT NULL,
    call_number         VARCHAR(31)     NOT NULL,
    reason              VARCHAR(255)    NOT NULL,
    timestamp           TIMESTAMP       NOT NULL DEFAULT NOW()
);
