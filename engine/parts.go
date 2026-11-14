package main

import (
	"fmt"

	"fnparse/parse"
)

type tocEntry struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Level int    `json:"level"`
	Index int    `json:"index"`
}

type statsPayload struct {
	Chapters int `json:"chapters"`
	Chars    int `json:"chars"`
	Parts    int `json:"parts"`
}

type part0Payload struct {
	Schema string       `json:"schema"`
	Sha1   string       `json:"sha1"`
	Format string       `json:"format"`
	Name   string       `json:"name"`
	Meta   parse.Meta   `json:"meta"`
	Toc    []tocEntry   `json:"toc"`
	Stats  statsPayload `json:"stats"`
}

type chapterPayload struct {
	Index      int           `json:"index"`
	ID         string        `json:"id"`
	Title      string        `json:"title"`
	Paragraphs []parse.Block `json:"paragraphs"`
}

type partNPayload struct {
	Schema   string           `json:"schema"`
	Sha1     string           `json:"sha1"`
	Seq      int              `json:"seq"`
	Total    int              `json:"total"`
	Chapters []chapterPayload `json:"chapters"`
}

func chapterID(i int) string {
	return fmt.Sprintf("ch%04d", i+1)
}

func tocOf(book *parse.Book) []tocEntry {
	toc := make([]tocEntry, 0, len(book.Chapters))
	for i, ch := range book.Chapters {
		title := ch.Title
		if title == "" {
			title = fmt.Sprintf("第 %d 章", i+1)
		}
		lv := ch.Level
		if lv < 1 {
			lv = 1
		}
		if lv > 6 {
			lv = 6
		}
		toc = append(toc, tocEntry{ID: chapterID(i), Title: title, Level: lv, Index: i})
	}
	return toc
}

// buildParts 生成 part0 + 正文分片；分片按 50 章或 partMax 字节切，不拆章。
func buildParts(book *parse.Book, sha1, format, name string, partMax int) ([][]byte, int, error) {
	if len(book.Chapters) == 0 {
		return nil, 0, fmt.Errorf("解析不出任何章节")
	}
	if len(book.Chapters) > 3000 {
		return nil, 0, fmt.Errorf("章节数超过 3000：%d", len(book.Chapters))
	}
	if partMax <= 0 {
		partMax = 2 << 20
	}
	toc := tocOf(book)

	// 先序列化每章，按体积分组
	type draft struct {
		payload []byte
		size    int
	}
	drafts := make([]draft, len(book.Chapters))
	for i, ch := range book.Chapters {
		cp := chapterPayload{
			Index:      i,
			ID:         chapterID(i),
			Title:      toc[i].Title,
			Paragraphs: ch.Paragraphs,
		}
		b, err := jsonNoEscape(cp)
		if err != nil {
			return nil, 0, err
		}
		drafts[i] = draft{payload: b, size: len(b)}
	}

	var groups [][]chapterPayload
	var group []chapterPayload
	groupBytes := 0
	for i := range drafts {
		if len(group) >= 50 || (groupBytes+drafts[i].size > partMax && len(group) > 0) {
			groups = append(groups, group)
			group = nil
			groupBytes = 0
		}
		group = append(group, chapterPayload{
			Index:      i,
			ID:         chapterID(i),
			Title:      toc[i].Title,
			Paragraphs: book.Chapters[i].Paragraphs,
		})
		groupBytes += drafts[i].size
	}
	if len(group) > 0 {
		groups = append(groups, group)
	}

	total := 1 + len(groups)
	part0 := part0Payload{
		Schema: parse.SCHEMA,
		Sha1:   sha1,
		Format: format,
		Name:   name,
		Meta:   book.Meta,
		Toc:    toc,
		Stats:  statsPayload{Chapters: len(toc), Chars: book.Chars, Parts: total},
	}
	b0, err := jsonNoEscape(part0)
	if err != nil {
		return nil, 0, err
	}
	parts := make([][]byte, 0, total)
	parts = append(parts, b0)
	for i, g := range groups {
		pn := partNPayload{
			Schema:   parse.SCHEMA,
			Sha1:     sha1,
			Seq:      i + 1,
			Total:    total,
			Chapters: g,
		}
		b, err := jsonNoEscape(pn)
		if err != nil {
			return nil, 0, err
		}
		parts = append(parts, b)
	}
	return parts, total, nil
}

func buildChapterBody(book *parse.Book, sha1 string, index int) ([]byte, error) {
	if index < 0 || index >= len(book.Chapters) {
		return nil, fmt.Errorf("章节号越界：%d / %d", index, len(book.Chapters))
	}
	ch := book.Chapters[index]
	title := ch.Title
	if title == "" {
		title = fmt.Sprintf("第 %d 章", index+1)
	}
	return jsonNoEscape(map[string]interface{}{
		"index":      index,
		"id":         chapterID(index),
		"title":      title,
		"paragraphs": ch.Paragraphs,
	})
}
