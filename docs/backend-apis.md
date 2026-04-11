# Backend APIs

Base URL for the HTTP gateway: `http://localhost:8081`

All browser-facing HTTP calls should go through the gateway. Internal gRPC services are listed separately at the end for reference.

## HTTP Gateway API

| Method | Path | Auth | Role / TOTP | Description |
|---|---|---|---|---|
| GET | `/healthz` | No | - | Health check |
| POST | `/api/login` | No | - | Login |
| POST | `/api/logout` | Yes | - | Logout |
| POST | `/api/token/refresh` | No | - | Refresh access token |
| POST | `/api/totp/setup/begin` | Yes | - | Start TOTP setup |
| POST | `/api/totp/setup/confirm` | Yes | - | Confirm TOTP setup |
| POST | `/api/totp/transaction-code` | Yes | `role:client` | Create transaction TOTP code |
| POST | `/api/totp/disable/begin` | Yes | - | Start TOTP disable flow |
| POST | `/api/totp/disable/confirm` | Yes | - | Confirm TOTP disable flow |
| GET | `/api/recipients` | Yes | `role:client` | List payment recipients |
| POST | `/api/recipients` | Yes | `role:client` | Create payment recipient |
| PUT | `/api/recipients/:id` | Yes | `role:client` | Update payment recipient |
| DELETE | `/api/recipients/:id` | Yes | `role:client` | Delete payment recipient |
| GET | `/api/transactions` | Yes | `role:client` | List transactions |
| GET | `/api/transactions/:id` | Yes | `role:client` | Get transaction by ID |
| GET | `/api/transactions/:id/pdf` | Yes | `role:client` | Generate transaction PDF |
| POST | `/api/transactions/payment` | Yes | `role:client` + TOTP | Payout money to another account |
| POST | `/api/transactions/transfer` | Yes | `role:client` + TOTP | Transfer money between own accounts |
| POST | `/api/password-reset/request` | No | - | Request password reset |
| POST | `/api/password-reset/confirm` | No | - | Confirm password reset |
| POST | `/api/clients` | Yes | `manage_clients` | Create client account |
| GET | `/api/clients` | Yes | `manage_clients` | List clients |
| PUT | `/api/clients/:id` | Yes | `manage_clients` | Update client |
| POST | `/api/employees` | Yes | `manage_employees` | Create employee account |
| GET | `/api/employees` | Yes | `manage_employees` | List employees |
| GET | `/api/employees/:employeeId` | Yes | `manage_employees` | Get employee by ID |
| DELETE | `/api/employees/:employeeId` | Yes | `manage_employees` | Delete employee |
| PATCH | `/api/employees/:employeeId` | Yes | `manage_employees` | Update employee |
| POST | `/api/companies` | Yes | `manage_companies` | Create company |
| GET | `/api/companies` | Yes | `manage_companies` | List companies |
| GET | `/api/companies/:id` | Yes | `manage_companies` | Get company by ID |
| PUT | `/api/companies/:id` | Yes | `manage_companies` | Update company |
| POST | `/api/accounts` | Yes | `manage_accounts` | Create account |
| GET | `/api/accounts` | Yes | `role:client\|employee` | List accounts |
| GET | `/api/accounts/:accountNumber` | Yes | `role:client\|employee` | Get account by number |
| PATCH | `/api/accounts/:accountNumber/name` | Yes | `role:client\|employee` | Update account name |
| PATCH | `/api/accounts/:accountNumber/limit` | Yes | `manage_accounts` + TOTP | Update account limits |
| GET | `/api/loans` | Yes | `role:client\|employee` | List loans |
| GET | `/api/loans/:loanNumber` | Yes | `role:client\|employee` | Get loan by number |
| POST | `/api/loan-requests` | Yes | `role:client` | Create loan request |
| GET | `/api/loan-requests` | Yes | `role:employee` | List loan requests |
| PATCH | `/api/loan-requests/:id/approve` | Yes | `manage_loans` | Approve loan request |
| PATCH | `/api/loan-requests/:id/reject` | Yes | `manage_loans` | Reject loan request |
| GET | `/api/cards` | Yes | `role:client` | List cards |
| POST | `/api/cards` | Yes | `role:client` | Request card |
| GET | `/api/cards/confirm` | Yes | `role:client` | Confirm card request |
| PATCH | `/api/cards/:cardNumber/block` | Yes | `role:client` | Block card |
| GET | `/api/exchange-rates` | Yes | `role:client` | Get exchange rates |
| POST | `/api/exchange/convert` | Yes | `role:client` | Convert money |

## HTTP Notes

| Item | Value |
|---|---|
| CORS allowed hosts | `localhost`, `127.0.0.1`, and hosts from `CORS_ALLOWED_HOSTS` in `.env` |
| TOTP header | `TOTP` |
| Allowed methods | `GET`, `POST`, `PUT`, `PATCH`, `DELETE`, `OPTIONS` |

## Internal gRPC Services

| Service | File | RPCs |
|---|---|---|
| `UserService` | `proto/user/user.proto` | `GetEmployeeById`, `GetEmployeeByEmail`, `GetEmployees`, `UpdateEmployee`, `DeleteEmployee`, `Login`, `Logout`, `Refresh`, `ValidateAccessToken`, `ValidateRefreshToken`, `RequestPasswordReset`, `RequestInitialPasswordSet`, `SetPasswordWithToken`, `CreateClientAccount`, `GetClients`, `UpdateClient`, `CreateEmployeeAccount`, `GetUserPermissions` |
| `TOTPService` | `proto/user/verification.proto` | `VerifyCode`, `CreateTransactionCode`, `EnrollBegin`, `EnrollConfirm`, `DisableBegin`, `DisableConfirm`, `Status` |
| `BankService` | `proto/bank/bank.proto` | `CreateCompany`, `GetCompanyById`, `GetCompanies`, `UpdateCompany`, `CreateCard`, `RequestCard`, `ConfirmCard`, `GetCards`, `BlockCard`, `GetPaymentRecipients`, `CreatePaymentRecipient`, `UpdatePaymentRecipient`, `DeletePaymentRecipient`, `GetTransactions`, `GetTransactionById`, `GenerateTransactionPdf`, `CreateAccount`, `UpdateAccountName`, `UpdateAccountLimits`, `ListAccounts`, `GetAccountDetails`, `ListClientTransactions`, `GetLoans`, `GetLoanByNumber`, `CreateLoanRequest`, `GetLoanRequests`, `ApproveLoanRequest`, `RejectLoanRequest`, `GetAllLoans`, `TransferMoneyBetweenAccounts`, `PayoutMoneyToOtherAccount`, `GetTransfersHistoryForUserEmail` |
| `ExchangeService` | `proto/exchange/exchange.proto` | `GetExchangeRates`, `ConvertMoney` |
| `NotificationService` | `proto/notification/notification.proto` | `SendConfirmationEmail`, `SendActivationEmail`, `SendPasswordResetEmail`, `SendInitialPasswordSetEmail`, `SendCardConfirmationEmail`, `SendCardCreatedEmail`, `SendLoanPaymentFailedEmail`, `SendTOTPDisableEmail` |

