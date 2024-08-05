-- name: GetRoom :one
select "id", "theme" 
from rooms 
where id = $1;


-- name GetRooms