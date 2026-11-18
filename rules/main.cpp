#include "core.hpp"
#include <iostream>
#include <string>
int main() {
 try {
  std::int64_t declared,shipping,tax,discount,count;
  if(!(std::cin>>declared>>shipping>>tax>>discount>>count) || count<0 || count>500) throw std::invalid_argument("invalid rules protocol header");
  std::vector<relay::Line> lines;
  for(std::int64_t i=0;i<count;++i) {
   relay::Line line{};
   if(!(std::cin>>line.quantity>>line.unit)) throw std::invalid_argument("invalid rules protocol line");
   lines.push_back(line);
  }
  std::string extra;
  if(std::cin>>extra) throw std::invalid_argument("unexpected rules protocol data");
  auto r=relay::reconcile(declared,shipping,tax,discount,lines);
  std::cout<<"{\"subtotal_minor\":"<<r.subtotal<<",\"total_minor\":"<<r.total<<",\"units\":"<<r.units<<",\"checked_lines\":"<<(r.checked_lines?"true":"false")<<"}\n";
  return 0;
 } catch(const std::exception& e) {std::cerr<<e.what()<<'\n';return 2;}
}
