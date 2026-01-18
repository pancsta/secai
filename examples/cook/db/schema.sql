CREATE TABLE characters (id integer PRIMARY KEY AUTOINCREMENT,result text NOT NULL);
CREATE TABLE ingredients (id integer PRIMARY KEY AUTOINCREMENT,name text NOT NULL,amount text NOT NULL);
CREATE TABLE jokes (id integer PRIMARY KEY AUTOINCREMENT,text text NOT NULL);
CREATE TABLE resources (id integer PRIMARY KEY AUTOINCREMENT,key text NOT NULL,value text NOT NULL);
