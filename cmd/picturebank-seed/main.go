package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"

	"github.com/Linka-masterskaya/zip-backend/internal/config"
	"github.com/Linka-masterskaya/zip-backend/internal/db"
	"github.com/Linka-masterskaya/zip-backend/internal/picturebank"
	"github.com/Linka-masterskaya/zip-backend/internal/storage"
	"github.com/google/uuid"
)

const defaultConfigPath = "config/config.dev.yml"

func main() {
	if err := run(context.Background(), os.Args[1:], os.Stdout); err != nil {
		log.Printf("picturebank-seed: %v", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, output io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: picturebank-seed <add|delete> [options]")
	}
	switch args[0] {
	case "add":
		return runAdd(ctx, args[1:], output)
	case "delete":
		return runDelete(ctx, args[1:], output)
	default:
		return fmt.Errorf("unknown command %q; expected add or delete", args[0])
	}
}

func runAdd(ctx context.Context, args []string, output io.Writer) error {
	flags := flag.NewFlagSet("add", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	configPath := flags.String("config", defaultConfigPath, "application config path")
	filePath := flags.String("file", "", "image file path")
	title := flags.String("title", "", "picture title")
	category := flags.String("category", "", "picture category")
	idRaw := flags.String("id", "", "optional stable UUID")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *filePath == "" {
		return errors.New("-file is required")
	}

	cfg, seeder, closeDB, err := openSeeder(*configPath)
	if err != nil {
		return err
	}
	defer closeDB()

	data, err := readSeedFile(*filePath, cfg.PicturesBank.MaxImageBytes)
	if err != nil {
		return err
	}
	var id uuid.UUID
	if *idRaw != "" {
		id, err = uuid.Parse(*idRaw)
		if err != nil {
			return fmt.Errorf("parse -id: %w", err)
		}
	}
	id, err = seeder.Add(ctx, picturebank.SeedInput{
		ID: id, Category: *category, Title: *title, Data: data,
	})
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(output, "added local picture %s\n", id)
	return err
}

func runDelete(ctx context.Context, args []string, output io.Writer) error {
	flags := flag.NewFlagSet("delete", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	configPath := flags.String("config", defaultConfigPath, "application config path")
	idRaw := flags.String("id", "", "picture UUID")
	if err := flags.Parse(args); err != nil {
		return err
	}
	id, err := uuid.Parse(*idRaw)
	if err != nil {
		return fmt.Errorf("valid -id is required: %w", err)
	}

	_, seeder, closeDB, err := openSeeder(*configPath)
	if err != nil {
		return err
	}
	defer closeDB()
	if err = seeder.Delete(ctx, id); err != nil {
		return err
	}
	_, err = fmt.Fprintf(output, "deleted local picture %s\n", id)
	return err
}

func openSeeder(configPath string) (*config.Config, *picturebank.Seeder, func(), error) {
	cfg, err := config.Load(configPath)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("load config: %w", err)
	}
	pool, err := db.New(cfg.DB)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("connect postgres: %w", err)
	}
	closeDB := func() { pool.Close() }
	objectStorage, err := storage.New(cfg.MinIO)
	if err != nil {
		closeDB()
		return nil, nil, nil, fmt.Errorf("connect minio: %w", err)
	}
	seeder, err := picturebank.NewSeeder(pool, objectStorage, cfg.PicturesBank.MaxImageBytes)
	if err != nil {
		closeDB()
		return nil, nil, nil, err
	}
	return cfg, seeder, closeDB, nil
}

func readSeedFile(filePath string, maxBytes int64) ([]byte, error) {
	if maxBytes <= 0 {
		return nil, errors.New("pictures_bank.max_image_bytes must be positive")
	}
	file, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("open picture file: %w", err)
	}
	data, readErr := io.ReadAll(io.LimitReader(file, maxBytes+1))
	closeErr := file.Close()
	if readErr != nil {
		return nil, fmt.Errorf("read picture file: %w", readErr)
	}
	if closeErr != nil {
		return nil, fmt.Errorf("close picture file: %w", closeErr)
	}
	if int64(len(data)) > maxBytes {
		return nil, fmt.Errorf("picture exceeds maximum size of %d bytes", maxBytes)
	}
	return data, nil
}
