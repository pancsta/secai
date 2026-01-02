CREATE TABLE prompts (id integer PRIMARY KEY AUTOINCREMENT,session_id text NOT NULL,agent text NOT NULL,state text NOT NULL,system text NOT NULL,history_len integer NOT NULL,request text NOT NULL,provider text NOT NULL,model text NOT NULL,response text,created_at datetime NOT NULL,mach_time_sum integer NOT NULL,mach_time text NOT NULL);
CREATE INDEX session ON prompts(session_id);
