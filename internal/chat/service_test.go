package chat

import (
	"encoding/json"
	"testing"
	"time"
)

func TestMessageJSONFormat(t *testing.T) {
	now := time.Now()
	original := Message{
		From:      "user123",
		Content:   "Привет, мир!",
		Timestamp: now,
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Ошибка маршалинга сообщения: %v", err)
	}

	t.Logf("JSON представление: %s", string(data))

	var restored Message
	err = json.Unmarshal(data, &restored)
	if err != nil {
		t.Fatalf("Ошибка анмаршалинга сообщения: %v", err)
	}

	if restored.From != original.From {
		t.Errorf("Поле From не совпадает: ожидалось %s, получено %s", original.From, restored.From)
	}
	if restored.Content != original.Content {
		t.Errorf("Поле Content не совпадает: ожидалось %s, получено %s", original.Content, restored.Content)
	}
	
	if restored.Timestamp.Unix() != original.Timestamp.Unix() {
		t.Errorf("Timestamp отличается более чем на секунду")
	}
}

func TestEmptyMessage(t *testing.T) {
	msg := Message{}
	data, _ := json.Marshal(msg)
	
	var restored Message
	json.Unmarshal(data, &restored)
	
	if restored.From != "" || restored.Content != "" {
		t.Error("Пустое сообщение должно оставаться пустым после сериализации")
	}
}