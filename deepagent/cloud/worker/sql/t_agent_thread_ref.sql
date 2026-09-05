CREATE TABLE t_agent_thread_ref (
  user_id BIGINT NOT NULL DEFAULT 0 COMMENT 'owner user id',
  session_id VARCHAR(128) NOT NULL COMMENT 'owner session id',
  thread_name VARCHAR(128) NOT NULL COMMENT 'friendly thread ref in one session, for example main alice bob parent',
  thread_id BIGINT UNSIGNED NOT NULL COMMENT 'agent Coordinator thread id',
  created_at DATETIME(6) NOT NULL COMMENT 'create time',
  updated_at DATETIME(6) NOT NULL COMMENT 'update time',
  PRIMARY KEY (user_id, session_id, thread_name),
  KEY idx_user_session_thread (user_id, session_id, thread_id),
  KEY idx_thread_id (thread_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci ROW_FORMAT=DYNAMIC COMMENT='DeepAgent worker session thread friendly ref table';
