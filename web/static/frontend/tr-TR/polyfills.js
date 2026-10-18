function s(t,e){for(let i=1,n=1;i<t.length;i++,n++)if(e[n]==="\\")n++;else if(t[i]===":")return i;throw new Error(`Unterminated $localize metadata block in "${e}".`)}var o=function(t,...e){if(o.translate){let n=o.translate(t,e);t=n[0],e=n[1]}let i=r(t[0],t.raw[0]);for(let n=1;n<t.length;n++)i+=e[n-1]+r(t[n],t.raw[n]);return i},l=":";function r(t,e){return e.charAt(0)===l?t.substring(s(t,e)+1):t}globalThis.$localize=o;(globalThis.$localize??={}).locale="tr-TR";
/**
 * @license
 * Copyright Google LLC All Rights Reserved.
 *
 * Use of this source code is governed by an MIT-style license that can be
 * found in the LICENSE file at https://angular.dev/license
 */

// THIS CODE IS GENERATED - DO NOT MODIFY.
  (function(global) {
    global.ng ??= {};
    global.ng.common ??= {};
    global.ng.common.locales ??= {};
    const u = undefined;
    function plural(val) {
const n = val;

if (n === 1)
    return 1;
return 5;
}
    global.ng.common.locales['tr'] = ["tr",[["öö","ös"],["ÖÖ","ÖS"]],[["ÖÖ","ÖS"]],[["P","P","S","Ç","P","C","C"],["Paz","Pzt","Sal","Çar","Per","Cum","Cmt"],["Pazar","Pazartesi","Salı","Çarşamba","Perşembe","Cuma","Cumartesi"],["Pa","Pt","Sa","Ça","Pe","Cu","Ct"]],u,[["O","Ş","M","N","M","H","T","A","E","E","K","A"],["Oca","Şub","Mar","Nis","May","Haz","Tem","Ağu","Eyl","Eki","Kas","Ara"],["Ocak","Şubat","Mart","Nisan","Mayıs","Haziran","Temmuz","Ağustos","Eylül","Ekim","Kasım","Aralık"]],u,[["MÖ","MS"],u,["Milattan Önce","Milattan Sonra"]],1,[6,0],["d.MM.y","d MMM y","d MMMM y","d MMMM y EEEE"],["HH:mm","HH:mm:ss","HH:mm:ss z","HH:mm:ss zzzz"],["{1} {0}",u,u,u],[",",".",";","%","+","-","E","×","‰","∞","NaN",":"],["#,##0.###","%#,##0","¤#,##0.00","#E0"],"TRY","₺","Türk lirası",{"AUD":["AU$","$"],"BYN":[u,"р."],"PHP":[u,"₱"],"RON":[u,"L"],"RUR":[u,"р."],"THB":["฿"],"TRY":["₺"],"TWD":["NT$"]},"ltr", plural, [[["gece","ö","sabah","öğleden önce","öğleden sonra","akşamüstü","akşam","gece"],["gece yarısı","öğle","sabah","öğleden önce","öğleden sonra","akşamüstü","akşam","gece"],u],[["gece yarısı","öğle","sabah","öğleden önce","öğleden sonra","akşamüstü","akşam","gece"],u,u],["00:00","12:00",["06:00","11:00"],["11:00","12:00"],["12:00","18:00"],["18:00","19:00"],["19:00","21:00"],["21:00","06:00"]]]];
  })(globalThis);
    
;;
/**i18n:fa3d7e19b336ec75*/
