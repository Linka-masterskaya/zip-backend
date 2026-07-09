package auth

// Repository описывает интерфейс для работы с базой данных.
type Repository interface {
}

type repository struct {
	db any
}

func NewRepository(dbPool any) Repository {
	return &repository{
		db: dbPool,
	}
}
