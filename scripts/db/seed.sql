INSERT INTO permissions (name)
VALUES
    ('admin'),
    ('trade_stocks'),
    ('view_stocks'),
    ('manage_contracts'),
    ('manage_insurance')
ON CONFLICT (name) DO NOTHING;

-- default admin (password: "Admin123!")
INSERT INTO employees (
    first_name, last_name, date_of_birth, gender, email,
    phone_number, address, username, password, salt_password,
    position, department, active
)
VALUES (
    'Admin', 'Admin', '1990-01-01', 'M', 'admin@banka.raf',
    '+381600000000', 'N/A', 'admin',
    '\x78db8c5a70624a77ff540ee38898086ab4db699e8905399b8a84c485cd7c4953'::BYTEA,
    '\xf5e2740f7afc0e0dd44968b7364fc102'::BYTEA,
    'Administrator', 'IT', true
)
ON CONFLICT (email) DO NOTHING;

-- give admin user the admin permission
INSERT INTO employee_permissions (employee_id, permission_id)
SELECT e.id, p.id
FROM employees e, permissions p
WHERE e.email = 'admin@banka.raf' AND p.name = 'admin'
ON CONFLICT DO NOTHING;

-- test client (password: "Test1234!")
INSERT INTO clients (
    first_name, last_name, date_of_birth, gender, email,
    phone_number, address, password, salt_password
)
VALUES (
    'Petar', 'Petrovic', '1990-05-20', 'M', 'petar@primer.raf',
    '+381645555555', 'Njegoseva 25',
    '\x0fadf52a4580cfebb99e61162139af3d3a6403c1d36b83e4962b721d1c8cbd0b'::BYTEA,
    '\x00'::BYTEA
)
ON CONFLICT (email) DO NOTHING;

INSERT INTO currencies (
    label, name, symbol, countries, description, active
)
VALUES (
    'EUR', 'Euro', '€',
    'Austria, Belgium, Bulgaria, Croatia, Cyprus, Estonia, Finland, France, Germany, Greece, Ireland, Italy, Latvia, Lithuania, Luxembourg, Malta, Netherlands, Portugal, Slovakia, Slovenia, Spain',
    'The euro (symbol: €; currency code: EUR) is the official currency of 21 of the 27 member states of the European Union. This group of states is officially known as the euro area or, more commonly, the eurozone. The euro is divided into 100 euro cents.[7][8]',
    TRUE
);

INSERT INTO accounts (number, name, owner, balance, created_by, valid_until, currency, owner_type, account_type, maintainance_cost, daily_limit, monthly_limit, daily_expenditure, monthly_expenditure)
VALUES(
'14159265358979323846', 'Arthur Dent', 1, 137, 1, '2029-12-31', 'EUR', 'personal', 'checking', 11, 2718, 100000, 10, 100);

Insert into activity_codes (code, sector, branch) values('whateve', 'Sector for bullshiting', 'Socially unprodictive banking branch');

Insert into companies(registered_id, name, tax_code, activity_code_id, address, owner_id) values(
31415926, 'Marvin the android corp', 42, 1, 'At the restaurant at the end of the universe', 1
);

Insert into cards (number, name, valid_until, account_number, cvv, card_limit) values(
'271828', 'Ford Perfect card', '2030-12-31', '14159265358979323846', '357', 10000
);

Insert into authorized_party (name, last_name, date_of_birth, gender, email, phone_number, address) values(
'Zaphod', 'Beeblebrox', '1999-10-29', 'M', 'zaphod_beeblebrox@heartofgold.com', '42424242', 'On a vogon ship'
);

Insert into payments (from_account, to_account, start_amount, end_amount, commission, recipient_id, transcaction_code, call_number, reason) values(
'14159265358979323846', '14159265358979323846', 42000, 1370, 50, 1, 11, '91803','Cuz I felt like it, there is no reason for you to have insight into my decision making process'
);

Insert into transfers (from_account, to_account, start_amount, end_amount, start_currency_id, exchange_rate, commission) values(
'14159265358979323846', '14159265358979323846', 11, 0, 1, 11.27, 3
);

Insert into payment_codes (code, description) values(
20, 'theory of Marx and Engels of the inevitability of a violent revolution refers to the bourgeois state'
);
