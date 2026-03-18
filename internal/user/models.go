package user

import (
	"time"
)

type (
	// Defining ALL 10 Enums which Dusan added
	owner_type         string
	account_type       string
	card_type          string
	card_status        string
	loan_type          string
	loan_status        string
	interest_rate_type string
	installment_status string
	employment_status  string

	// Note, unlike type aliease these are all destinct types, only
	// their underlying type is string.
	// I could have only defined and used one type, but
	// this seems more legible at this moment.
)

// These are the enums which will be used by the models below
const (
	// ownet_type enum
	Personal owner_type = "personal"
	Business owner_type = "business"

	// account_type enum
	Checking account_type = "checking"
	Foreign  owner_type   = "foreign"

	// card_type enum
	Debit  card_type = "debit"
	Credit card_type = "credit"

	// card_status enum
	Active      card_status = "active"
	Blocked     card_status = "blocked"
	Deactivated card_status = "deactivated"

	// loan_type enum
	Cash        loan_type = "cash"
	Mortgage    loan_type = "mortgage"
	Car         loan_type = "car"
	Refinancing loan_type = "refinancing"
	Student     loan_type = "student"

	// loan_status enum
	Approved loan_status = "approved"
	Rejected loan_status = "rejected"
	Paid     loan_status = "paid"
	Late     loan_status = "late"

	// interest_rate_type enum
	Fixed    interest_rate_type = "fixed"
	Variable interest_rate_type = "variable"

	// installment_status enum
	Installment_Paid installment_status = "paid" // Prevent conflict with loan_status Paid
	Due              installment_status = "due"
	Insallment_Late  installment_status = "late"

	// Eemployment_status
	Full_time  employment_status = "full_time"
	Temporary  employment_status = "temporary"
	Unemployed employment_status = "unemployed"
)

type (
	Client struct {
		Id            uint64    `gorm:"column:id;type:bigint;not null;primaryKey"`
		First_name    string    `gorm:"column:first_name;type:varchar(100);not null"`
		Last_name     string    `gorm:"column:last_name;type:varchar(100);not null"`
		Date_of_birth time.Time `gorm:"column:date_of_birth;type:date;not null"`
		Gender        string    `gorm:"column:gender;type:varchar(1);not null"`
		Email         string    `gorm:"column:email;type:varchar(255);unique;not null"`
		Phone_number  string    `gorm:"column:phone_number;type:varchar(20);not null"`
		Address       string    `gorm:"column:address;type:varchar(255);not null"`
		Password      []byte    `gorm:"column:password;type:bytea;not null"`
		Salt_password []byte    `gorm:"column:salt_password;type:bytea;not null"`
		Created_at    time.Time `gorm:"column:created_at;not null;autoCreateTime"`
		Updated_at    time.Time `gorm:"column:updated_at;not null;autoUpdateTime"`
	}

	Employee struct {
		Id            uint64    `gorm:"column:id;type:bigint;not null;primaryKey"`
		First_name    string    `gorm:"column:first_name;type:varchar(100);not null"`
		Last_name     string    `gorm:"column:last_name;type:varchar(100);not null"`
		Date_of_birth time.Time `gorm:"column:date_of_birth;type:date;not null"`

		Gender        string       `gorm:"column:gender;type:varchar(1);not null"`
		Email         string       `gorm:"column:email;type:varchar(255);unique;not null"`
		Phone_number  string       `gorm:"column:phone_number;type:varchar(20); not null"`
		Address       string       `gorm:"column:address;type:varchar(255);not null"`
		Username      string       `gorm:"column:username;type:varchar(100);unique;not null"`
		Password      []byte       `gorm:"column:password;type:bytea;not null"`
		Salt_password []byte       `gorm:"column:salt_password;type:bytea;not null"`
		Position      string       `gorm:"column:position;type:varchar(100);not null"`
		Department    string       `gorm:"column:department;type:varchar(100);not null"`
		Active        bool         `gorm:"column:active;type:bool; not null"`
		Created_at    time.Time    `gorm:"column:created_at;not null;autoCreateTime"`
		Updated_at    time.Time    `gorm:"column:updated_at;not null;autoUpdateTime"`
		Permissions   []Permission `gorm:"many2many:employee_permissions;joinForeignKey:Employee_id;joinReferences:PermissionId"`
	}

	Permission struct {
		Id   uint64 `gorm:"column:id;type:bigint;not null;primaryKey"`
		Name string `gorm:"column:name;type:varchar(100);not null"`
	}
	EmployeePermissions struct {
		EmployeeId   uint64 `gorm:"column:employee_id;not null"`
		PermissionId uint64 `gorm:"column:permission_id;not null"`
	}

	Get_employees struct {
		Id              int    `gorm:"column:id;type:bigint;not null;primaryKey"`
		First_name      string `gorm:"column:first_name;type:varchar(100);not null"`
		Last_name       string `gorm:"column:last_name;type:varchar(100);not null"`
		Email           string `gorm:"column:email;type:varchar(255);unique;not null"`
		Position        string `gorm:"column:position;type:varchar(100);not null"`
		Phone_number    string `gorm:"column:phone_number;type:varchar(20);not null"`
		Active          bool   `gorm:"column:active;type:bool; not null"`
		Permission_id   int64  `gorm:"column:Permission_id;type:bigint;not null;primaryKey"`
		Permission_name string `gorm:"column:Permission_name;type:varchar(100);not null"`
	}

	Currency struct {
		Id          int64  `gorm:"column:id;type:bigserial;not null;primaryKey"`
		Label       string `gorm:"column:label;type:varchar(8);not null;unique"`
		Name        string `gorm:"column:name;type:varchar(64);not null"`
		Symbol      string `gorm:"column:symbol;type:varchar(8);not null"`
		Countries   string `gorm:"column:countries;type:TEXT;not null"`
		Description string `gorm:"column:description;type:varchar(1023);not null"`
		Active      bool   `gorm:"column:active;type:BOOLEAN;not null;default:true"`
	}

	Account struct {
		Id                  int64        `gorm:"column:id;type:bigserial;not null;primaryKey"`
		Number              string       `gorm:"column:number;type:varchar(20);not null;unique"`
		Name                string       `gorm:"column:name;type:varchar(127);not null"`
		Owner               int64        `gorm:"column:owner;type:bigint;not null;references clients(id)"`
		Balance             int64        `gorm:"column:balance;type:bigint;not null;default 0"`
		Created_by          int64        `gorm:"column:created_by;type:bigint;not null;references employees(id)"`
		Created_at          time.Time    `gorm:"column:created_at;not null;autoCreateTime"`
		Valid_until         time.Time    `gorm:"column:valid_until;not null"`
		Currency            string       `gorm:"column:currency;type:varchar(8);not null;references currencies(label)"`
		Active              bool         `gorm:"column:active;type:BOOLEAN;not null;default:false"`
		Owner_type          owner_type   `gorm:"column:owner_type;type:owner_type;not null"`
		Account_type        account_type `gorm:"column:account_type;type:account_type;not null"`
		Maintainance_cost   int64        `gorm:"column:maintainance_cost;type:bigint;not null"`
		Daily_limit         int64        `gorm:"column:daily_limit;type:bigint"`
		Monthly_limit       int64        `gorm:"column:monthly_limit;type:bigint"`
		Daily_expenditure   int64        `gorm:"column:daily_expenditure;type:bigint"`
		Monthly_expenditure int64        `gorm:"column:monthly_expenditure;type:bigint"`
	}

	ActivityCode struct {
		Id     int64  `gorm:"column:id;type:bigserial;not null;primaryKey"`
		Code   string `gorm:"column:code;type:varchar(7);not null;unique"`
		Sector string `gorm:"column:sector;type:varchar(127);not null"`
		Branch string `gorm:"column:branch;type:varchar(255);not null"`
	}

	Company struct {
		Id               int64  `gorm:"column:id;type:bigserial;not null;primaryKey"`
		Registered_id    int64  `gorm:"column:registered_id;type:bigint;not null;unique"`
		Name             string `gorm:"column:name;type:varchar(127);not null"`
		Tax_code         int64  `gorm:"column:tax_code;type:bigint;not null"`
		Activity_code_id int64  `gorm:"column:activity_code_id;type:int64;references activity_codes(id)"`
		Address          string `gorm:"column:address;type:varchar(255);not null"`
		Owner_id         int64  `gorm:"column:owner_id;type:bigint;not null;references clients(id)"`
	}

	Card struct {
		Id             int64       `gorm:"column:id;type:bigserial;not null;primaryKey"`
		Number         string      `gorm:"column:number;type:varchar(20);not null;unique"`
		Type           card_type   `gorm:"column:type;type:card_type;not null;default:'debit'"`
		Name           string      `gorm:"column:name;type:varchar(127);not null"`
		Creation_date  time.Time   `gorm:"column:creation_date;not null;autoCreateTime"`
		Valid_until    time.Time   `gorm:"column:created_at;not null;autoCreateTime"`
		Account_number string      `gorm:"column:account_number;type:varchar(20);references accounts(number)"`
		Cvv            string      `gorm:"column:cvv;type:varchar(7);not null"`
		Card_limit     int64       `gorm:"column:card_limit;type:bigint"`
		Status         card_status `gorm:"column:status;type:card_status;not null;default 'active'"`
	}

	AuthorizedParty struct {
		Id            int64     `gorm:"column:id;type:bigserial;not null;primaryKey"`
		Name          string    `gorm:"column:name;type:varchar(63);not null"`
		Last_name     string    `gorm:"column:last_name;type:varchar(63);not null"`
		Date_of_birth time.Time `gorm:"column:date_of_birth;type:date;not null"`
		Gender        string    `gorm:"column:gender;type:varchar(7);not null"`
		Email         string    `gorm:"column:email;type:varchar(127);unique;not null"`
		Phone_number  string    `gorm:"column:phone_number;type:varchar(15);not null"`
		Address       string    `gorm:"column:address;type:varchar(255);not null"`
	}

	Payment struct {
		Transaction_id   int64     `gorm:"column:transaction_id;type:bigserial;not null;primaryKey"`
		From_account     string    `gorm:"column:from_account;type:varchar(20);references accounts(number)"`
		To_account       string    `gorm:"column:to_account;type:varchar(20);references accounts(number)"`
		Start_amount     int64     `gorm:"column:start_amount;type:bigint;not null"`
		End_amount       int64     `gorm:"column:end_amount;type:bigint;not null"`
		Commission       int64     `gorm:"column:comission;type:bigint;not null"`
		Recipient_id     int64     `gorm:"column:recipient_id;type:bigint;references clients(id)"`
		Transaction_code int       `gorm:"column:transaction_code;type:int;not null"`
		Call_number      string    `gorm:"column:call_number;type:varchar(31);not null"`
		Reason           string    `gorm:"column:reason;type:varchar(255);not null"`
		Timestamp        time.Time `gorm:"column:timestamp;not null;autoCreateTime"`
	}

	Transfer struct {
		Transaction_id    int64     `gorm:"column:transaction_id;type:bigserial;not null;primaryKey"`
		From_account      string    `gorm:"column:from_account;type:varchar(20);references accounts(number)"`
		To_account        string    `gorm:"column:to_account;type:varchar(20);references accounts(number)"`
		Start_amount      int64     `gorm:"column:start_amount;type:bigint;not null"`
		End_amount        int64     `gorm:"column:end_amount;type:bigint;not null"`
		Start_currency_id int64     `gorm:"column:start_currency_id;type:bigint;references currencies(id)"`
		Exchange_rate     float64   `gorm:"column:exchange_rate;type:decimal(20,2)"`
		Commission        int64     `gorm:"column:commission;type:bigint;not null"`
		Timestamp         time.Time `gorm:"column:timestamp;not null;autoCreateTime"`
	}

	PaymentCode struct {
		Code        int64  `gorm:"column:code;type:bigint;not null;primaryKey"`
		Description string `gorm:"column:description;type:varchar(255);not null"`
	}

	Loan struct {
		Id                 int64              `gorm:"column:id;type:bigserial;not null;primaryKey"`
		Account_id         int64              `gorm:"column:account_id;type:bigint;references accounts(id)"`
		Amount             float64            `gorm:"column:amount;type:decimal(12,2);not null"`
		Currency_id        int64              `gorm:"column:currency_id;type:bigserial;references currencies(id)"`
		Installments       int64              `gorm:"column:installments;type:bigint;not null"`
		Interest_rate      float32            `gorm:"column:interest_rate;type:decimal(5,2);not null"`
		Date_signed        time.Time          `gorm:"column:date_signed;type:date;not null;"`
		Date_end           time.Time          `gorm:"column:date_end;type:date;not null;"`
		Monthly_payment    float64            `gorm:"column:monthly_payment;type:decimal(20,2);not null"`
		Next_payment_due   time.Time          `gorm:"column:next_payment_due;type:date;not null;"`
		Remaining_debt     float64            `gorm:"column:remaining_debt;type:decimal(20,2);not null"`
		Type               loan_type          `gorm:"column:type;type:loan_type;not null"`
		Loan_status        loan_status        `gorm:"column:loan_status;type:loan_status;not null;default 'approved'"`
		Interest_rate_type interest_rate_type `gorm:"column:interest_rate_type;type:interest_rate_type;not null"`
	}

	LoanInstallment struct {
		Id                 int64              `gorm:"column:id;type:bigserial;not null;primaryKey"`
		Loan_id            int64              `gorm:"column:loan_id;type:bigint;references loans(id)"`
		Installment_amount float32            `gorm:"column:installment_amount;type:decimal(20,2);not null"`
		Interest_rate      float32            `gorm:"column:interest_rate;type:decimal(5,2);not null"`
		Currency_id        int64              `gorm:"column:currency_id;type:bigserial;references currencies(id)"`
		Due_date           time.Time          `gorm:"column:due_date;type:date;not null"`
		Paid_date          time.Time          `gorm:"column:paid_date;type:date;not null"`
		Status             installment_status `gorm:"column:installment_status;type:installment;not null;default 'due'"`
	}

	LoanRequest struct {
		Id                      int64              `gorm:"column:id;type:bigserial;not null;primaryKey"`
		Type                    loan_type          `gorm:"column:type;type:loan_type;not null"`
		Interest_rate_type      interest_rate_type `gorm:"column:interest_rate_type;type:interest_rate_type;not null"`
		Purpose                 string             `gorm:"column:purpose;type:varchar(1023);not null"`
		Monthly_installment     string             `gorm:"column:monthly_installment;type:decimal(20,2);not null"`
		Currency_id             int64              `gorm:"column:currency_id;type:bigint;references currencies(id)"`
		Amount                  float64            `gorm:"column:amount;type:decimal(20,2);not null"`
		Employment_status       employment_status  `gorm:"column:employment_status;type:employment_status;not null"`
		Current_employment_time int64              `gorm:"column:current_employment_time;type:bigint;not null"`
		Phone_contact           string             `gorm:"column:phone_contact;type:varchar(20);not null"`
		Account_id              int64              `gorm:"column:account_id;type:bigint;references accounts(id)"`
	}

	VerificationCode struct {
		Id             int64     `gorm:"column:id;type:bigserial;not null;primaryKey"`
		Client_id      int64     `gorm:"column:client_id;type:bigint;references clients(id)"`
		Transaction_id int64     `gorm:"column:transaction_id;type:bigint;references payments(transaction_id)"`
		Valid_until    time.Time `gorm:"column:created_at;not null"`
		Tries          int       `gorm:"column:tries;type:int;not null;default 0"`
		Valid          bool      `gorm:"column:valid;type:boolean;not null;default true"`
		Used           bool      `gorm:"column:used;type:boolean;not null;default false"`
	}
)

func (Client) TableName() string {
	return "clients"
}

func (Employee) TableName() string {
	return "employees"
}

func (Currency) TableName() string {
	return "currencies"
}

func (Account) TableName() string {
	return "accounts"
}

func (ActivityCode) TableName() string {
	return "activity_codes"
}

func (Company) TableName() string {
	return "companies"
}

func (Card) TableName() string {
	return "cards"
}

func (AuthorizedParty) Table_name() string {
	return "authorized_party"
}

func (Payment) TableName() string {
	return "payments"
}

func (Transfer) TableName() string {
	return "transfers"
}

func (Loan) TableName() string {
	return "loans"
}

func (LoanInstallment) TableName() string {
	return "loan_installment"
}

func (LoanRequest) TableName() string {
	return "loan_request"
}

func (VerificationCode) TableName() string {
	return "loan_request"
}

func (Permission) TableName() string {
	return "permissions"
}

func (EmployeePermissions) TableName() string {
	return "employee_permissions"
}
