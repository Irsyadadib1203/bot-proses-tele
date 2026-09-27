CREATE TABLE IF NOT EXISTS batch_orders (
    id BIGINT PRIMARY KEY AUTO_INCREMENT,
    telegram_user_id BIGINT NOT NULL,
    telegram_chat_id BIGINT NOT NULL,
    product_code VARCHAR(50) NOT NULL,
    target_id VARCHAR(100) NOT NULL,
    qty INT NOT NULL,
    status VARCHAR(20) NOT NULL DEFAULT 'processing', -- processing, completed
    success_count INT NOT NULL DEFAULT 0,
    failed_count INT NOT NULL DEFAULT 0,
    manual_review_count INT NOT NULL DEFAULT 0,
    created_at DATETIME NOT NULL,
    completed_at DATETIME NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS batch_order_items (
    id BIGINT PRIMARY KEY AUTO_INCREMENT,
    batch_id BIGINT NOT NULL,
    sequence_no INT NOT NULL,
    idempotency_key VARCHAR(100) NOT NULL,
    sn VARCHAR(255) NULL,
    provider_ref VARCHAR(255) NULL,
    status VARCHAR(30) NOT NULL DEFAULT 'pending', -- pending, in_progress, success, failed, needs_manual_review
    error_message TEXT NULL,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
    UNIQUE KEY uniq_batch_seq (batch_id, sequence_no),
    INDEX idx_batch_status (batch_id, status),
    CONSTRAINT fk_batch_order FOREIGN KEY (batch_id) REFERENCES batch_orders(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
