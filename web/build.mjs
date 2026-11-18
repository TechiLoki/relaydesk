import {copyFileSync,mkdirSync} from 'node:fs';
mkdirSync(new URL('./dist/',import.meta.url),{recursive:true});
for(const file of ['index.html','style.css'])copyFileSync(new URL(file,import.meta.url),new URL('dist/'+file,import.meta.url));
