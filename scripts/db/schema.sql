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
    CHECK (action_type IN ('reset', 'initial_set'))
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

CREATE TYPE owner_type AS ENUM ('personal', 'business');
CREATE TYPE account_type AS ENUM ('checking', 'foreign');

CREATE TABLE IF NOT EXISTS accounts (
    id                  BIGSERIAL       PRIMARY KEY,
    number              VARCHAR(20)     NOT NULL,
    name                VARCHAR(127)    NOT NULL,
    owner               BIGINT          NOT NULL REFERENCES clients(id) ON DELETE CASCADE, -- cascade delete??
    balance             BIGINT          NOT NULL DEFAULT 0,
    created_by          BIGINT          REFERENCES employees(id) ON DELETE SET NULL,
    created_at          DATE            NOT NULL DEFAULT CURRENT_DATE,
    valid_until         DATE            NOT NULL,
    currency            VARCHAR(8)      NOT NULL REFERENCES currencies(label) ON UPDATE CASCADE ON DELETE RESTRICT,
    active              BOOLEAN         NOT NULL DEFAULT FALSE,
    owner_type          owner_type      NOT NULL,
    account_type        account_type   NOT NULL,
    maintainance_cost   BIGINT          NOT NULL,
    daily_limit         BIGINT,
    monthly_limit       BIGINT,
    daily_expenditure   BIGINT,
    monthly_expenditure BIGINT,
    UNIQUE(number)
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

CREATE TYPE card_type AS ENUM ('debit', 'credit');
CREATE TYPE card_status AS ENUM ('active', 'blocked', 'deactivated');
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

CREATE TABLE IF NOT EXISTS transfers (
    transaction_id      BIGSERIAL       PRIMARY KEY,
    from_account        VARCHAR(20)     REFERENCES accounts(number),
    to_account          VARCHAR(20)     REFERENCES accounts(number),
    start_amount        BIGINT          NOT NULL,
    end_amount          BIGINT          NOT NULL,
    start_currency_id   BIGINT          REFERENCES currencies(id) ON UPDATE CASCADE ON DELETE RESTRICT,
    exchange_rate       DECIMAL(20,2),
    commission          BIGINT          NOT NULL,
    status              VARCHAR(20)     NOT NULL DEFAULT 'realized' CHECK (status IN ('realized', 'rejected', 'pending')),
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
    amount              DECIMAL(12, 2)      NOT NULL,
    currency_id         BIGSERIAL           REFERENCES currencies(id) ON UPDATE CASCADE ON DELETE RESTRICT,
    installments        BIGINT              NOT NULL,
    interest_rate       DECIMAL (5, 2)      NOT NULL,
    date_signed         DATE                NOT NULL,
    date_end            DATE                NOT NULL,
    monthly_payment     DECIMAL(20, 2)      NOT NULL,
    next_payment_due    DATE                NOT NULL,
    remaining_debt      DECIMAL(20, 2)      NOT NULL,
    type                loan_type           NOT NULL,
    loan_status         loan_status         NOT NULL DEFAULT 'approved',
    interest_rate_type  interest_rate_type  NOT NULL
);

CREATE TYPE installment_status AS ENUM ('paid', 'due', 'late');

CREATE TABLE IF NOT EXISTS loan_installment (
    id                  BIGSERIAL           PRIMARY KEY,
    loan_id             BIGINT              REFERENCES loans(id) ON UPDATE CASCADE ON DELETE CASCADE,
    installment_amount  DECIMAL(20,2)       NOT NULL,
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
    amount              DECIMAL(20, 2)       NOT NULL,
    repayment_period    BIGINT               NOT NULL,
    account_id          BIGINT               REFERENCES accounts(id) ON UPDATE CASCADE ON DELETE RESTRICT,
    status              loan_request_status  NOT NULL DEFAULT 'pending',
    submission_date     TIMESTAMP            NOT NULL DEFAULT NOW(),
    purpose             VARCHAR(255),
    salary              DECIMAL(20, 2),
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

CREATE TABLE IF NOT EXISTS exchange_rates (
    currency_code VARCHAR(3)     PRIMARY KEY,
    rate_to_rsd   DECIMAL(20, 6) NOT NULL,
    updated_at    TIMESTAMP      NOT NULL DEFAULT NOW(),
    valid_until   TIMESTAMP      NOT NULL DEFAULT NOW()
);
