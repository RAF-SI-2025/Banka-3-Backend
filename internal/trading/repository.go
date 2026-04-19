package trading

func (s *Server) ListExchangesRecord() ([]Exchange, error) {
	var out []Exchange
	if err := s.db.Find(&out).Error; err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Server) ListListingsRecord() ([]Listing, error) {
	var out []Listing
	if err := s.db.Find(&out).Error; err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Server) CreateOrderPlacerRecord(p *OrderPlacer) error {
	return s.db.Create(p).Error
}

func (s *Server) CreateOrderRecord(o *Order) error {
	return s.db.Create(o).Error
}

func (s *Server) GetOrderRecord(id int64) (*Order, error) {
	var o Order
	if err := s.db.First(&o, id).Error; err != nil {
		return nil, err
	}
	return &o, nil
}
