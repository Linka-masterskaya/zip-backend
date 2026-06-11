## **Git Flow — Проект Linka**

Проект использует упрощённый **GitHub Flow** с единственной основной веткой `main`. Все изменения вливаются в `main` через Pull Request.

---

### **Стратегия Ветвления**

1. **Основная ветка (`main`)**

   * **Назначение:** Единственная постоянная ветка. Хранит актуальный рабочий код.
   * **Правила:**

     * Прямые коммиты в `main` запрещены.
     * Изменения попадают в `main` только через Pull Request после code review.
     * Вся новая разработка начинается от `main`.

2. **Фиче-ветки (`feature/AB-<task-number>-short-description`)**

   * **Назначение:** Разработка новой функциональности или исправление багов.
   * **Правила:**

     * Создаются от `main`.
     * После завершения работы создаётся PR для слияния в `main`.
     * Ветка удаляется после слияния.
   * **Примеры:**

     * `feature/AB-1-create-house`
     * `feature/AB-3-user-registration`

3. **Hotfix-ветки (`hotfix/AB-<task-number>-short-description`)**

   * **Назначение:** Срочные исправления критических ошибок.
   * **Правила:**

     * Создаются от `main`.
     * После исправления вливаются в `main` через PR.
     * Ветка удаляется после слияния.
   * **Пример:**

     * `hotfix/AB-105-fix-auth`

---

### **Процесс Работы**

1. **Создание ветки**

   ```bash
   git checkout main
   git pull origin main
   git checkout -b feature/AB-<task-number>-description
   ```

2. **Разработка и коммиты**

   ```bash
   git add .
   git commit -m "[AB-<number>] Short description of changes"
   ```

3. **Публикация ветки и создание PR в `main`**

   ```bash
   git push origin feature/AB-<task-number>-description
   ```

   * PR: `feature/AB-...` → `main`

---

### **Пример Рабочего Процесса**

**Разработчик Анна выполняет задачу AB-3: Реализация метода получения дома**

```bash
git checkout main
git pull origin main
git checkout -b feature/AB-3-get-house
# работа над кодом
git commit -m "[AB-3] Implement method GET /houses/{id}"
git push origin feature/AB-3-get-house
```

На GitHub создаётся PR из `feature/AB-3-get-house` в `main`.

---

### **Hotfix-процесс**

```bash
git checkout main
git pull origin main
git checkout -b hotfix/AB-105-fix-auth-error
# правки
git commit -m "[AB-105] Fix critical auth error"
git push origin hotfix/AB-105-fix-auth-error
```

PR → `main`.

---

### **Правила Именования и Коммитов**

* **Фиче-ветки:** `feature/AB-<task-number>-description`
* **Hotfix-ветки:** `hotfix/AB-<task-number>-description`
* **Коммиты:** `"[AB-<number>] Short description"`

---

### **Структура репозитория**

```
main
├── feature/AB-1-create-house
├── feature/AB-2-user-registration
├── hotfix/AB-105-fix-auth-error
```

---

### **Заключение**

Одна постоянная ветка `main` упрощает процесс: нет синхронизации между `dev` и `main`, нет двойных PR. Вся разработка начинается от `main`, все изменения вливаются в `main` через PR после review.
