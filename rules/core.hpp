#pragma once
#include <cstdint>
#include <stdexcept>
#include <vector>
namespace relay {
constexpr std::int64_t limit = 1'000'000'000'000LL;
struct Line {std::int64_t quantity, unit;};
struct Result {std::int64_t subtotal, total, units; bool checked_lines;};
inline void bounded(std::int64_t n) {
 if(n<0 || n>limit) throw std::invalid_argument("money outside supported range");
}
inline std::int64_t add(std::int64_t a,std::int64_t b) {
 bounded(a);bounded(b);
 if(a>limit-b) throw std::invalid_argument("money addition exceeds supported range");
 return a+b;
}
inline Result reconcile(std::int64_t declared,std::int64_t shipping,std::int64_t tax,std::int64_t discount,const std::vector<Line>& lines) {
 bounded(declared);bounded(shipping);bounded(tax);bounded(discount);
 if(lines.size()>500) throw std::invalid_argument("too many line items");
 // Stripe Checkout snapshots omit line items. Validate their bounded total, but
 // explicitly report that line-by-line reconciliation was not possible.
 if(lines.empty()) return {0,declared,0,false};
 std::int64_t subtotal=0,units=0;
 for(const auto& line:lines) {
  if(line.quantity<1 || line.quantity>10000) throw std::invalid_argument("invalid quantity");
  bounded(line.unit);
  if(line.unit>limit/line.quantity) throw std::invalid_argument("line total exceeds supported range");
  subtotal=add(subtotal,line.quantity*line.unit);
  units+=line.quantity;
 }
 auto gross=add(add(subtotal,shipping),tax);
 if(discount>gross) throw std::invalid_argument("discount exceeds total");
 auto total=gross-discount;
 if(total!=declared) throw std::invalid_argument("declared total does not match line items, shipping, tax and discount");
 return {subtotal,total,units,true};
}
}
