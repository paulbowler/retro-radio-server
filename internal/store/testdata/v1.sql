CREATE TABLE schema_migrations(version INTEGER PRIMARY KEY);
INSERT INTO schema_migrations VALUES(1);
CREATE TABLE stations(id TEXT PRIMARY KEY,name TEXT NOT NULL,url TEXT NOT NULL,stream_id TEXT NOT NULL UNIQUE,codec TEXT NOT NULL,bitrate INTEGER NOT NULL);
CREATE TABLE devices(id TEXT PRIMARY KEY,name TEXT NOT NULL,manufacturer TEXT NOT NULL,model TEXT NOT NULL,protocol TEXT NOT NULL,firmware TEXT NOT NULL,ip TEXT NOT NULL,last_seen TEXT NOT NULL,capabilities TEXT NOT NULL);
CREATE TABLE favourites(device_id TEXT NOT NULL REFERENCES devices(id),station_id TEXT NOT NULL REFERENCES stations(id),PRIMARY KEY(device_id,station_id));
CREATE TABLE activity(id INTEGER PRIMARY KEY,time TEXT NOT NULL,device TEXT NOT NULL,kind TEXT NOT NULL,detail TEXT NOT NULL);
INSERT INTO stations VALUES('1001','Smooth Radio','https://media-ice.musicradio.com/SmoothUKMP3','existing-opaque-stream-id','MP3',128);
INSERT INTO devices VALUES('legacy-device','Kitchen','pure','Unverified Frontier XML radio','frontierxml','8','192.168.1.99','2026-10-03T12:00:00Z','{"supports_http":true,"supports_mp3":true,"supports_icy_metadata":true}');
INSERT INTO favourites VALUES('legacy-device','1001');
