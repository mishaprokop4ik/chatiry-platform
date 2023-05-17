package repository

import (
	"Kurajj/internal/models"
	zlog "Kurajj/pkg/logger"
	"context"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"github.com/samber/lo"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const defaultUserImage = "https://charity-platform.s3.amazonaws.com/images/png-transparent-default-avatar-thumbnail.png"

type User struct {
	DBConnector *Connector
	Filer
	Notifier
}

func (u *User) SetSession(ctx context.Context, userID uint, session models.MemberSession) error {
	session.MemberID = userID
	err := u.DBConnector.DB.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "member_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"refresh_token", "expires_at"}),
	}).Create(&session).
		WithContext(ctx).
		Error

	return err
}

func (u *User) GetByRefreshToken(ctx context.Context, token string) (models.User, error) {
	session := models.MemberSession{}
	err := u.DBConnector.DB.Where("refresh_token = ?", token).First(&session).WithContext(ctx).Error
	if err != nil && errors.Is(err, gorm.ErrRecordNotFound) {
		return models.User{}, fmt.Errorf("the token may be expired")
	} else if err != nil {
		return models.User{}, err
	}
	member := models.User{}
	err = u.DBConnector.DB.First(&member, session.MemberID).WithContext(ctx).Error
	if err != nil {
		return models.User{}, err
	}
	memberSearch := []models.MemberSearch{}
	err = u.DBConnector.DB.
		Where("member_id = ?", member.ID).
		Find(&memberSearch).
		WithContext(ctx).
		Error

	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return models.User{}, err
	}
	member.UserSearchValues = memberSearch

	for i, searchValue := range member.UserSearchValues {
		searchValues := []models.SearchValue{}
		err = u.DBConnector.DB.Where("member_search_id = ?", searchValue.ID).
			Find(&searchValues).
			WithContext(ctx).
			Error

		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			zlog.Log.Error(err, "got an error while getting search values")
			continue
		}
		member.UserSearchValues[i].Values = searchValues
	}

	member.RefreshToken = session.RefreshToken

	notifications, err := u.Notifier.GetByMember(ctx, member.ID)
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return models.User{}, err
	}

	member.TransactionNotification = notifications

	return member, err
}

func (u *User) GetUserInfo(ctx context.Context, id uint) (models.User, error) {
	user := models.User{}
	err := u.DBConnector.DB.
		Where("id = ?", id).
		Where("is_deleted = ?", false).
		Where("is_activated = ?", true).
		First(&user).
		WithContext(ctx).
		Error
	if err != nil {
		return models.User{}, err
	}
	user.Password = ""

	return user, nil
}

func NewUser(config AWSConfig, DBConnector *Connector) *User {
	return &User{DBConnector: DBConnector, Filer: NewFile(config), Notifier: NewTransactionNotification(DBConnector)}
}

func (u *User) IsEmailTaken(ctx context.Context, email string) (bool, error) {
	var exists bool
	err := u.DBConnector.DB.Model(&models.User{}).
		Select("count(*) > 0").
		Where("email = ?", email).
		Find(&exists).
		Error

	return exists, err
}

func (u *User) CreateUser(ctx context.Context, user models.User) (uint, error) {
	if user.Image != nil {
		fileName, err := uuid.NewUUID()
		if err != nil {
			return 0, err
		}
		filePath, err := u.Filer.Upload(ctx, fmt.Sprintf("%s.%s", fileName.String(), user.FileType), user.Image)
		if err != nil {
			zlog.Log.Error(err, "could not upload file")
			return 0, err
		}
		user.AvatarImagePath = filePath
	} else if user.AvatarImagePath == "" {
		user.AvatarImagePath = defaultUserImage
	}
	err := u.DBConnector.DB.
		Create(&user).
		WithContext(ctx).Error
	return user.ID, err
}

func (u *User) GetUserAuthentication(ctx context.Context, email, password string) (models.User, error) {
	member := models.User{}
	resp := u.DBConnector.DB.
		Where("password = ? AND email = ?", password, email).
		Where("is_deleted = ?", false).
		Where("is_activated = ?", true).
		First(&member).
		WithContext(ctx)

	if errors.Is(resp.Error, gorm.ErrRecordNotFound) {
		return member, fmt.Errorf("could not find with input email: %s; it may be besause the password is incorrect", email)
	}

	memberSearch := []models.MemberSearch{}
	err := u.DBConnector.DB.
		Where("member_id = ?", member.ID).
		Find(&memberSearch).
		WithContext(ctx).
		Error

	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return models.User{}, err
	}
	member.UserSearchValues = memberSearch

	for i, searchValue := range member.UserSearchValues {
		searchValues := []models.SearchValue{}
		err = u.DBConnector.DB.Where("member_search_id = ?", searchValue.ID).
			Find(&searchValues).
			WithContext(ctx).
			Error

		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			zlog.Log.Error(err, "got an error while getting search values")
			continue
		}
		member.UserSearchValues[i].Values = searchValues
	}

	notifications, err := u.Notifier.GetByMember(ctx, member.ID)
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return models.User{}, err
	}

	member.TransactionNotification = notifications

	return member, resp.Error
}

func (u *User) GetEntity(ctx context.Context, email, password string, isAdmin, isDeleted bool) (models.User, error) {
	member := models.User{}
	err := u.DBConnector.DB.
		WithContext(ctx).
		Where("email = ?", email).
		Where("password = ?", password).
		Where("is_admin = ?", isAdmin).
		Where("is_deleted = ?", isDeleted).
		First(&member).
		Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return models.User{}, fmt.Errorf("could not found an entity ")
	}

	memberSearch := []models.MemberSearch{}
	err = u.DBConnector.DB.
		Where("member_id = ?", member.ID).
		Find(&memberSearch).
		WithContext(ctx).
		Error

	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return models.User{}, err
	}
	member.UserSearchValues = memberSearch

	for i, searchValue := range member.UserSearchValues {
		searchValues := []models.SearchValue{}
		err = u.DBConnector.DB.Where("member_search_id = ?", searchValue.ID).
			Find(&searchValues).
			WithContext(ctx).
			Error

		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			zlog.Log.Error(err, "got an error while getting search values")
			continue
		}
		member.UserSearchValues[i].Values = searchValues
	}

	notifications, err := u.Notifier.GetByMember(ctx, member.ID)
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return models.User{}, err
	}

	member.TransactionNotification = notifications

	return member, err
}

func (u *User) DeleteUser(ctx context.Context, id uint) error {
	panic("")
}

func (u *User) UpsertUser(ctx context.Context, values map[string]any) error {
	//return := u.DBConnector.DB.Omit()
	panic("")
}

func (u *User) UpdateUserByEmail(ctx context.Context, email string, values map[string]any) error {
	return u.DBConnector.DB.
		Model(&models.User{}).
		Select(lo.Keys(values)).
		Where("email = ?", email).
		Updates(values).
		Error
}
