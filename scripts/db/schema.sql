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

CREATE TYPE account_type AS ENUM ('personal', 'business');

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
    type                account_type    NOT NULL,
    maintainance_cost   BIGINT          NOT NULL,
    daily_limit         BIGINT,
    monthly_limit       BIGINT,
    daily_expenditure   BIGINT,
    monthly_expenditure BIGINT,
    -- TODO dodati tip za da li je devizni ili tekuci
);
